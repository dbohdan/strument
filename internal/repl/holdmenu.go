package repl

import (
	"errors"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/readline"
)

// holdMenu answers a Ctrl-C during a turn when someone can be asked.
//
// It holds rather than cancels. The reply being written keeps streaming,
// into held output; the turn stops at its next step boundary, before a
// reply's tool calls run and before a new request is sent. The menu then
// offers what a person who pressed Ctrl-C usually means: carry on, stop, or
// say something. A message is not a steer that cuts the reply off: it waits
// for the step boundary and goes in as the user's words beside the tool
// results, or, when the turn ends first, it is the next turn.
//
// The hard stop is still here: s ends the turn now, cancelling a reply that
// is still being written, and a second Ctrl-C at the menu exits, as the
// chord always has. SIGUSR1 never comes here; it is a program's interrupt,
// and no one is at a menu.
func (r *REPL) holdMenu() {
	r.askMu.Lock()
	defer r.askMu.Unlock()
	r.held.hold()
	r.coder.Hold()

	r.out.clearWaiting()
	r.out.showCursor()
	fmt.Fprintln(r.opts.Stdout)
	r.out.Toolf("‹paused› The current step will finish with the output held. No new steps while paused.")
	r.out.Toolf("Enter to resume, s to stop this turn, or type a message to send before the next step. Ctrl-C again to exit.")
	// A Ctrl-C at the menu that came too late to complete the chord asks
	// again, so the next one exits: taking it as Enter would carry on the
	// turn, the one thing a second Ctrl-C does not mean. A read that fails
	// otherwise, at the end of input, carries on.
	line, err := rlAsker{r}.readAsk(" > ")
	for errors.Is(err, readline.ErrInterrupt) {
		line, err = rlAsker{r}.readAsk(" > ")
	}

	message, stop := "", false
	switch t := strings.TrimSpace(line); {
	case err != nil:
	case t == "":
	case strings.EqualFold(t, "s") || strings.EqualFold(t, "stop"):
		stop = true
		r.out.Toolf("Stopping the turn.")
	default:
		message = t
		r.out.Toolf("Queued for the model's next step.")
	}

	r.held.release()
	r.coder.Release(message, stop)
	if stop {
		// A reply still being written is cut off now; one already done
		// stops at the boundary it is held at.
		r.coder.InterruptSend()
	}
	if r.interactive() {
		r.out.hideCursor()
	}
}

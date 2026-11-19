package coder

import (
	"context"
	"strings"
	"sync"

	"dbohdan.com/strument/internal/llm"
)

// A Ctrl-C in the REPL holds the turn instead of cancelling the request. The
// reply being written keeps streaming, into held output, but nothing new
// starts: a reply's tool calls wait before they run, and the next request
// waits before it is sent. The user then continues, stops, or types a message
// for the model, which goes in at the next step boundary. What does not
// happen is the agent carrying on, for as long as the user reads a menu,
// behind a transcript nobody can see.
//
// The hard interrupt, InterruptSend, is unchanged and still what SIGUSR1 and
// a terminal with no one to ask get.

// turnHold is the gate and the messages waiting at it.
type turnHold struct {
	mu       sync.Mutex
	held     bool
	released chan struct{} // closed when the hold is released
	stop     bool          // the user chose to stop the turn
	queued   []string      // messages for the next step boundary
}

// queuedNote frames a message delivered at a step boundary, so the model
// reads it as the user's words that arrived while it worked, not as the
// answer to something it asked.
const queuedNote = "The user sent this while you were working. It arrived after your last step, " +
	"so take it into account from here on."

// unrunText answers a tool call the user stopped before it ran.
const unrunText = "Not run: the user stopped the turn before this ran."

// stoppedNote ends a turn the user stopped between steps, after the last
// step's results, so the next message is not read as an answer to them.
const stoppedNote = "The user stopped the turn here, after the results above. Nothing after them ran."

// Hold stops the turn at its next step boundary until Release. Holding a turn
// that is already held changes nothing.
func (c *Coder) Hold() {
	c.hold.mu.Lock()
	defer c.hold.mu.Unlock()
	if !c.hold.held {
		c.hold.held = true
		c.hold.released = make(chan struct{})
	}
}

// Release lets a held turn go on. A message, when given, waits for the next
// step boundary; stop ends the turn there instead. The caller cancels a
// reply still streaming itself, with InterruptSend, when it means to stop.
func (c *Coder) Release(message string, stop bool) {
	c.hold.mu.Lock()
	defer c.hold.mu.Unlock()
	if m := strings.TrimSpace(message); m != "" {
		c.hold.queued = append(c.hold.queued, m)
	}
	if stop {
		c.hold.stop = true
	}
	if c.hold.held {
		c.hold.held = false
		close(c.hold.released)
	}
}

// TakeQueued returns the messages waiting for a step boundary, joined, and
// forgets them. The REPL takes what is left when a turn ends before one, as
// the next turn's message.
func (c *Coder) TakeQueued() string {
	c.hold.mu.Lock()
	defer c.hold.mu.Unlock()
	q := strings.Join(c.hold.queued, "\n\n")
	c.hold.queued = nil
	return q
}

// stopRequested reports whether the user chose to stop this turn.
func (c *Coder) stopRequested() bool {
	c.hold.mu.Lock()
	defer c.hold.mu.Unlock()
	return c.hold.stop
}

// resetHold clears a stop left from the previous turn. Queued messages
// survive: the REPL takes them between turns.
func (c *Coder) resetHold() {
	c.hold.mu.Lock()
	defer c.hold.mu.Unlock()
	c.hold.stop = false
}

// waitHold blocks while the turn is held, or until the send is cancelled.
// The caller asks stopRequested what the user decided.
func (c *Coder) waitHold(ctx context.Context) {
	c.hold.mu.Lock()
	ch := c.hold.released
	held := c.hold.held
	c.hold.mu.Unlock()
	if held {
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}
}

// deliverQueued makes the waiting messages the next send's user message, and
// shows them where the user can see them arrive. It reports false when there
// were none.
func (c *Coder) deliverQueued() (string, bool) {
	q := c.TakeQueued()
	if q == "" {
		return "", false
	}
	c.resumeInPlace = false
	c.framingQueued = true
	c.Out.Toolf("‹your message, delivered› %s", q)
	return q, true
}

// answerUnrun answers every tool call of the reply just streamed without
// running it, so the conversation stays well-formed: each call has its result.
func (c *Coder) answerUnrun(text string) {
	results := toolResults{}
	for _, tc := range c.stream.toolCalls {
		results.setText(tc.ID, text)
	}
	c.appendToolResults(results)
}

// queuedMessage is a message delivered at a step boundary: the user's own
// words, after a note saying when they arrived.
func queuedMessage(m llm.Message) llm.Message {
	return withSeam(m, queuedNote)
}

// stopAtBoundary ends a turn stopped between steps as a Ctrl-C ends one: the
// results so far are kept, a note says where it stopped, and the outcome is
// the interrupt's, so the REPL says that the work was kept.
func (c *Coder) stopAtBoundary() {
	c.curMessages = append(c.curMessages, llm.HarnessNote(stoppedNote))
	c.lastSendOutcome = OutcomeInterrupted
}

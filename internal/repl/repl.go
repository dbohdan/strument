package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/readline"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/llm"
)

// ctrlCWindow is the double-Ctrl-C chord window (§1.2).
const ctrlCWindow = 2 * time.Second

// Options configures a REPL. Zero values mean production defaults; the
// seams (Stdin/Stdout, IsTerminal, Notify, Exit, Now) exist for the pty
// tests.
type Options struct {
	Coder      *coder.Coder
	Config     *config.Config
	ModelAlias string // alias of the active model, for /model display

	// Git enables /undo and /diff; nil outside a repository (--no-git).
	Git *gitrepo.Repo

	// History records each turn to a markdown transcript; nil disables it
	// (--no-history).
	History *history.Writer

	// MakeClient builds a client when /model switches providers.
	MakeClient func(*config.Model) llm.ModelClient

	Color       bool
	HistoryFile string

	Stdin  io.Reader // default os.Stdin
	Stdout io.Writer // default os.Stdout
	Stderr io.Writer // default os.Stderr

	// IsTerminal overrides terminal detection (readline renders line
	// editing only on real terminals).
	IsTerminal func() bool
	// MakeRaw/ExitRaw override raw-mode handling; readline's default
	// operates on the process's stdin, which tests replace with a pty.
	MakeRaw func() error
	ExitRaw func() error
	// Notify subscribes ch to SIGINT for the duration of a turn and
	// returns the unsubscribe. Default: os/signal.
	Notify func(ch chan<- os.Signal) (stop func())
	// Exit ends the process on the second in-turn Ctrl-C. Default: os.Exit.
	Exit func(code int)
	// Now is the chord clock. Default: time.Now.
	Now func() time.Time
}

// REPL is the interactive session driver.
type REPL struct {
	opts  Options
	coder *coder.Coder
	rl    *readline.Instance
	out   *termOutput

	mu        sync.Mutex
	lastCtrlC time.Time

	// One-shot /ask and /code restore the previous format after one turn.
	oneShotPending bool
	oneShotRestore string
}

// New builds the REPL, wires the coder's Out to the live renderer, and
// prepares readline. Close releases the terminal.
func New(opts Options) (*REPL, error) {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Notify == nil {
		opts.Notify = func(ch chan<- os.Signal) func() {
			signal.Notify(ch, os.Interrupt)
			return func() { signal.Stop(ch) }
		}
	}
	if opts.Exit == nil {
		opts.Exit = os.Exit
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	r := &REPL{opts: opts, coder: opts.Coder}
	r.out = &termOutput{w: opts.Stdout, color: opts.Color}
	r.coder.Out = r.out

	cfg := &readline.Config{
		Prompt:          r.prompt(),
		HistoryFile:     opts.HistoryFile,
		InterruptPrompt: "\n",
		EOFPrompt:       "\n",
		AutoComplete:    r.completer(),
		Stdin:           opts.Stdin,
		Stdout:          opts.Stdout,
		Stderr:          opts.Stderr,
	}
	if opts.IsTerminal != nil {
		cfg.FuncIsTerminal = opts.IsTerminal
	}
	if opts.MakeRaw != nil {
		cfg.FuncMakeRaw = opts.MakeRaw
	}
	if opts.ExitRaw != nil {
		cfg.FuncExitRaw = opts.ExitRaw
	}
	rl, err := readline.NewFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	r.rl = rl
	return r, nil
}

// Close releases the readline terminal state.
func (r *REPL) Close() error { return r.rl.Close() }

// Confirmer returns a coder.Confirmer that asks on this REPL's terminal;
// wrap it in an AutoConfirmer for --yes handling.
func (r *REPL) Confirmer() coder.Confirmer { return rlConfirmer{r} }

func (r *REPL) prompt() string {
	label := "> "
	if r.coder.EditFormat() == "ask" {
		label = "ask> "
	}
	if r.opts.Color {
		return "\x1b[1m" + label + "\x1b[0m"
	}
	return label
}

func (r *REPL) printf(format string, args ...any) {
	fmt.Fprintf(r.opts.Stdout, format+"\n", args...)
}

// chord records a Ctrl-C and reports whether it is the second one within
// the window (=> exit). Shared between the prompt and in-turn paths, like
// aider's io.last_keyboard_interrupt.
func (r *REPL) chord() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.opts.Now()
	second := !r.lastCtrlC.IsZero() && now.Sub(r.lastCtrlC) < ctrlCWindow
	r.lastCtrlC = now
	return second
}

// Run is the main loop: getInput -> dispatch/runOne -> undo hint (§1.2).
// It returns when the user exits (/exit, Ctrl-D, or a Ctrl-C chord at the
// prompt).
func (r *REPL) Run(ctx context.Context) error {
	for {
		line, err := r.rl.ReadLine()
		switch {
		case errors.Is(err, readline.ErrInterrupt):
			// Readline already cleared the line.
			if r.chord() {
				return nil
			}
			r.printf("^C again to exit")
			continue
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			msg, quit := r.dispatch(ctx, line)
			if quit {
				return nil
			}
			if msg == "" {
				continue
			}
			line = msg
		}

		r.runTurn(ctx, line)
		if r.oneShotPending {
			r.coder.SetEditFormat(r.oneShotRestore)
			r.rl.SetPrompt(r.prompt())
			r.oneShotPending = false
		}
		r.showUndoHint()
	}
}

// runTurn runs one user message through the coder with in-turn Ctrl-C
// handling: the first cancels the send, the second within 2s exits.
func (r *REPL) runTurn(ctx context.Context, message string) {
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sig := make(chan os.Signal, 1)
	stop := r.opts.Notify(sig)
	defer stop()

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-sig:
				if r.chord() {
					r.opts.Exit(130)
					return
				}
				fmt.Fprintln(r.opts.Stderr, "\n^C again to exit")
				cancel()
			case <-done:
				return
			}
		}
	}()

	sentBefore, recvBefore := r.coder.SessionTokens()
	costBefore, _ := r.coder.SessionCost()

	answer := r.coder.Run(tctx, message)

	if r.opts.History != nil {
		sentAfter, recvAfter := r.coder.SessionTokens()
		costAfter, known := r.coder.SessionCost()
		if err := r.opts.History.Append(history.Turn{
			Model:          r.coder.Model.Slug,
			TokensSent:     sentAfter - sentBefore,
			TokensReceived: recvAfter - recvBefore,
			Cost:           costAfter - costBefore,
			CostKnown:      known,
			User:           message,
			Assistant:      answer,
		}); err != nil {
			r.out.Warningf("Could not write chat history: %v", err)
		}
	}
}

// showUndoHint mentions /undo after a message that moved HEAD (aider's
// show_undo_hint: compare the pre-message HEAD with the current one).
func (r *REPL) showUndoHint() {
	if r.coder.Repo == nil {
		return
	}
	cbm := r.coder.CommitsBeforeMessage()
	if len(cbm) > 0 && cbm[len(cbm)-1] != r.coder.Repo.HeadSHA() {
		r.printf("You can use /undo to undo and discard each strument commit.")
	}
}

// rlConfirmer asks yes/no questions through the REPL's readline so prompt
// input and confirm input share one reader.
type rlConfirmer struct{ r *REPL }

func (cf rlConfirmer) Confirm(req coder.ConfirmRequest) (bool, bool) {
	r := cf.r
	if req.Subject != "" {
		r.printf("%s", req.Subject)
	}

	// aider's confirm_ask defaults: yes, unless an explicit yes is
	// required.
	suffix, def := " (Y/n", true
	if req.ExplicitYesRequired {
		suffix, def = " (y/N", false
	}
	if req.AllowNever {
		suffix += "/d=don't ask again"
	}
	suffix += ") "

	cfg := r.rl.GetConfig()
	cfg.Prompt = req.Prompt + suffix
	cfg.HistoryLimit = -1 // y/n answers stay out of the input history
	cfg.AutoComplete = nil

	line, err := r.rl.ReadLineWithConfig(cfg)
	if err != nil {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def, false
	case "y", "yes":
		return true, false
	case "d", "never", "don't":
		return false, true
	default:
		return false, false
	}
}

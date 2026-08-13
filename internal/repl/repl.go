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

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/readline"
	"dbohdan.com/strument/internal/render"
)

// ctrlCWindow is the double-Ctrl-C chord window.
const ctrlCWindow = 2 * time.Second

// Options configures a REPL. Zero values mean production defaults; the
// seams (Stdin/Stdout, IsTerminal, Notify, Exit, Now) exist for the pty
// tests.
type Options struct {
	Coder      *coder.Coder
	Config     *config.Config
	ModelAlias string // alias of the active model, for /model display

	// ResumeNote is a banner line naming what a previous session left pinned,
	// "" when nothing was restored. The banner lists the files either way; this
	// says where they came from, so restored context is never invisible.
	ResumeNote string

	// SaveResume records what a resume would restore, called after any command
	// that changes it. nil when the session leaves no trace (--no-history), so
	// the REPL needs no flag of its own.
	SaveResume func(alias string)

	// Git enables /undo and /diff; nil outside a repository (--no-git).
	Git *gitrepo.Repo

	// History records each turn to a markdown transcript; nil disables it
	// (--no-history).
	History *history.Writer

	// MakeClient builds a client when /model switches providers.
	MakeClient func(*config.Model) llm.ModelClient

	// ReloadConfig re-reads config.star for /reload, using the same options as
	// the initial load. nil disables /reload.
	ReloadConfig func() (*config.Config, error)

	Color       bool
	HistoryFile string

	// Version fills the opening banner's "Strument v…" line.
	Version string
	// Theme is the color palette; the zero value defaults to DefaultTheme.
	Theme render.Theme
	// GetSize reports the terminal width for the horizontal rules (and is
	// shared with readline so its completion grid wraps at the same width).
	// nil falls back to 80 columns.
	GetSize func() (width, height int)

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
	if opts.Theme == (render.Theme{}) {
		opts.Theme = render.DefaultTheme()
	}

	r := &REPL{opts: opts, coder: opts.Coder}
	r.out = &termOutput{w: opts.Stdout, color: opts.Color, theme: opts.Theme, width: r.termWidth()}
	if opts.Config != nil {
		r.out.Thinking = coder.ThinkingDisplay(opts.Config.ReasoningDisplay)
	}
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
		// We record input history ourselves (saveHistory) so we can keep
		// every substantive line — prompts, /ask, /add, /model — but drop
		// the pure session-enders that recalling would never help with.
		DisableAutoSaveHistory: true,
	}
	// Paint the typed line in the user-input color, like aider (the "> "
	// prompt is already colored). The escapes are zero-width and cursor math
	// is computed from the buffer, so end-of-line typing stays aligned. A
	// fresh slice is returned so the buffer readline passes in is never
	// mutated.
	if opts.Color && opts.Theme.UserInput != "" {
		green := []rune("\x1b[" + opts.Theme.UserInput + "m")
		reset := []rune("\x1b[0m")
		cfg.Painter = func(line []rune, _ int) []rune {
			if len(line) == 0 {
				return line
			}
			out := make([]rune, 0, len(green)+len(line)+len(reset))
			out = append(out, green...)
			out = append(out, line...)
			out = append(out, reset...)
			return out
		}
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
	if opts.GetSize != nil {
		cfg.FuncGetSize = opts.GetSize
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
	return r.sgr(r.opts.Theme.UserInput) + label + r.sgr("0")
}

// refreshTrailer recomputes the commit trailer for the given model so a
// /model switch is reflected in later auto-commits. opts.Git and the coder's
// repo are the same object, so this reaches the commit path.
func (r *REPL) refreshTrailer(m *config.Model) {
	if r.opts.Git != nil {
		r.opts.Git.CommitTrailer = gitrepo.Trailer(m.ReadableName())
	}
}

// sgr wraps an SGR parameter string, honoring --no-color and skipping empty
// codes (which would otherwise emit a bare reset).
func (r *REPL) sgr(codes string) string {
	if !r.opts.Color || codes == "" {
		return ""
	}
	return "\x1b[" + codes + "m"
}

// termWidth reports the terminal width for the rules, or 80 when unknown.
func (r *REPL) termWidth() int {
	if r.opts.GetSize != nil {
		if w, _ := r.opts.GetSize(); w > 0 {
			return w
		}
	}
	return 80
}

// interactive reports whether we are driving a real terminal (the banner,
// rules, and file listing are shown only then, like aider's pretty mode).
func (r *REPL) interactive() bool {
	return r.opts.IsTerminal == nil || r.opts.IsTerminal()
}

// announce prints the opening banner once at session start, mirroring
// aider's get_announcements: version, model, git repo, repo
// map, and the initially-added files.
func (r *REPL) announce() {
	if !r.interactive() {
		return
	}
	r.printf("Strument v%s", r.opts.Version)
	r.printf("Model: %s", r.coder.Model.QualifiedSlug())
	if r.opts.Git != nil {
		r.printf("Git repo: .git with %d files", len(r.opts.Git.TrackedFiles()))
	} else {
		r.printf("Git repo: none")
	}
	if r.opts.ResumeNote != "" {
		r.printf("%s", r.opts.ResumeNote)
	}
	// The parse layer is what symbol and the after-an-edit check are built on.
	// The banner used to report the repo map's token budget here, which stopped
	// meaning anything when the map left the prompt: nothing spends those
	// tokens. What a user can act on is whether the parser is available at all.
	if r.coder.HasParser() {
		r.printf("Language parser: on, for /symbol")
	} else {
		r.printf("Language parser: off")
	}
	for _, f := range r.coder.ChatFiles() {
		r.printf("Added %s for editing.", f)
	}
}

// renderPromptHeader draws the full-width rule and the in-chat file listing
// above each prompt (aider's io.rule + format_files_for_input).
func (r *REPL) renderPromptHeader() {
	if !r.interactive() {
		return
	}
	// A solid box-drawing rule, matching aider's io.rule (Rich console.rule,
	// which uses "─"). The dashed hyphen is reserved for markdown rules — aider
	// draws those through Rich's Markdown, whose thematic break is "-".
	r.printf("%s%s%s", r.sgr(r.opts.Theme.UserInput), strings.Repeat("─", r.termWidth()), r.sgr("0"))
	for _, f := range r.coder.ReadOnlyFiles() {
		r.printf("%s (read-only)", f)
	}
	for _, f := range r.coder.ChatFiles() {
		r.printf("%s", f)
	}
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

// historyExcluded is the set of commands not worth recalling: the pure
// session-enders. Everything else — prompts, /ask, /add, /model, /run —
// is saved so up-arrow can bring it back.
var historyExcluded = map[string]bool{"/exit": true, "/quit": true}

// saveHistory records a submitted line in the input history unless it is a
// session-ender. The command word is matched exactly (so "/exit now" and a
// message beginning "exit" are still saved).
func (r *REPL) saveHistory(line string) {
	cmd, _, _ := strings.Cut(line, " ")
	if historyExcluded[cmd] {
		return
	}
	_ = r.rl.SaveToHistory(line)
}

// Run is the main loop: getInput -> dispatch/runOne -> undo hint.
// It returns when the user exits (/exit, Ctrl-D, or a Ctrl-C chord at the
// prompt).
func (r *REPL) Run(ctx context.Context) error {
	r.announce()
	for {
		r.renderPromptHeader()
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
		r.saveHistory(line)

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

// withinTurn runs fn with the in-turn scaffolding shared by a normal turn and a
// one-off /btw: a cancelable context, cursor restore, the double-Ctrl-C chord
// (first cancels the send, the second within 2s exits), and the "Waiting for
// <model>" cue. It returns fn's result.
func (r *REPL) withinTurn(ctx context.Context, fn func(context.Context) string) string {
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The cursor is hidden while output streams (termOutput); restore it on
	// the way out even if the turn panics. FlushStream already restores it on
	// the normal and single-Ctrl-C paths, so this is a no-op there.
	defer r.out.showCursor()

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
					// os.Exit skips the defer above, so restore the cursor
					// here before the hard exit (aider's on-exit guard).
					if r.opts.Color {
						fmt.Fprint(r.opts.Stderr, "\x1b[?25h")
					}
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

	// Show "Waiting for <model>" until the first streamed byte, so a
	// slow-to-wake model doesn't look hung (aider's WaitingSpinner). Only
	// interactively; the first stream event erases it.
	if r.interactive() {
		r.out.hideCursor()
		r.out.startWaiting(r.coder.Model.QualifiedSlug())
	}
	return fn(tctx)
}

// runAside runs a /btw one-off question with the same in-turn scaffolding as a
// normal turn, but records no history — the exchange is not part of the chat.
func (r *REPL) runAside(ctx context.Context, question string) {
	r.withinTurn(ctx, func(tctx context.Context) string {
		return r.coder.RunAside(tctx, question)
	})
}

// runTurn runs one user message through the coder with in-turn Ctrl-C
// handling: the first cancels the send, the second within 2s exits.
func (r *REPL) runTurn(ctx context.Context, message string) {
	sentBefore, recvBefore := r.coder.SessionTokens()
	costBefore, _ := r.coder.SessionCost()

	answer := r.withinTurn(ctx, func(tctx context.Context) string {
		return r.coder.Run(tctx, message)
	})

	if r.opts.History != nil {
		sentAfter, recvAfter := r.coder.SessionTokens()
		costAfter, known := r.coder.SessionCost()
		if err := r.opts.History.Append(history.Turn{
			Model:          r.coder.Model.QualifiedSlug(),
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
		r.printf("You can use /undo to undo and discard each Strument commit.")
	}
}

// confirmSuffix is the answer hint appended to a confirmation prompt.
//
// Every prompt defaults to yes, the shell gate included. aider defaulted that
// one to no and Strument followed; the reason to diverge is that the cost falls
// on a human reaching for a key, where Enter is easier than Y even for a touch
// typist, and friction in the common case is precisely what erodes a prompt
// into reflex. What buys the yes is the purpose line above it: a prompt worth
// reading can afford a cheap answer, one that is not cannot. RequiresYesShell
// still keeps plain --yes from covering the shell gate, which is a question
// about flags rather than about what Enter means.
//
// Pulled out of Confirm because that is the one part of the prompt a test
// cannot see: readline writes it straight to the terminal, so in the scripted
// sessions it never reaches the captured output.
func confirmSuffix(req coder.ConfirmRequest) string {
	if req.Group != "" {
		return " (Y/n/a=all turn) "
	}
	return " (Y/n) "
}

// rlConfirmer asks yes/no questions through the REPL's readline so prompt
// input and confirm input share one reader.
type rlConfirmer struct{ r *REPL }

func (cf rlConfirmer) Confirm(req coder.ConfirmRequest) coder.ConfirmResult {
	r := cf.r
	switch {
	case req.Command != "":
		// The purpose is the model's claim about the command, so it sits above
		// it, recessive: it is narration, and the command is the thing that has
		// to be read. An absent purpose is shown rather than skipped — the model
		// was asked for one, and that it gave none is worth weighing before
		// answering.
		if req.Purpose != "" {
			r.out.Toolf("‹shell› %s", req.Purpose)
		} else {
			r.out.Warningf("‹shell› (no purpose given)")
		}
		// "$ " is the shape runChecks prints an argv in, so the two shell
		// surfaces read alike. The color deliberately is not: there the check is
		// routine, here it is the decision.
		r.out.Printf("$ %s", req.Command)
	case req.Subject != "":
		r.out.Printf("%s", req.Subject)
	}

	cfg := r.rl.GetConfig()
	cfg.Prompt = req.Prompt + confirmSuffix(req)
	cfg.HistoryLimit = -1 // y/n answers stay out of the input history
	cfg.AutoComplete = nil

	line, err := r.rl.ReadLineWithConfig(cfg)
	if err != nil {
		return coder.ConfirmResult{}
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return coder.ConfirmResult{Yes: true}
	case "y", "yes":
		return coder.ConfirmResult{Yes: true}
	case "a", "always":
		return coder.ConfirmResult{AlwaysThisTurn: true}
	default:
		return coder.ConfirmResult{}
	}
}

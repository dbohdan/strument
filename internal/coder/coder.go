package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repomap"
	"dbohdan.com/strument/internal/workspace"
)

// A turn has two separate budgets, because the two kinds of re-send mean
// different things. An error reflection is the model recovering from its own
// mistake and should stay rare; a work step is the model reading a tool result
// and carrying on, which is ordinary progress.
const (
	// maxAutoCheck bounds the rounds the harness itself starts by running the
	// project's checks. It is small and separate on purpose: a model caught in a
	// fix-break cycle should hand back to the human rather than spend the work
	// budget on rounds nobody asked for.
	maxAutoCheck = 3
)

// Coder is the chat loop state.
type Coder struct {
	Root  string
	Model *config.Model

	// Options.
	DryRun               bool
	AutoCommits          bool
	SuggestShellCommands bool // false gates execution too
	Stream               bool
	PrefillSupported     bool // continuation on finish_reason=length
	ExamplesAsSysMsg     bool
	UseSystemPrompt      bool
	SystemPromptPrefix   string
	ChatLanguage         string

	// MaxSteps is the work-step budget per turn — a checkpoint, not a wall.
	// On exhaustion the user is shown what the turn has done and asked
	// whether to keep going. Configurable; the default (25) is set by New.
	MaxSteps int
	// MaxErrorReflections is the error-reflection budget per turn. An error
	// reflection is the model recovering from its own mistake and should
	// stay rare. Configurable; the default (3) is set by New.
	MaxErrorReflections int

	// Ports.
	Client     llm.ModelClient
	Summarizer *ChatSummary // nil => chat-history summarization disabled
	Tokens     TokenCounter
	Confirm    Confirmer
	Runner     CommandRunner
	Repo       Repo
	Clock      Clock
	Out        Output
	RepoMap    *repomap.RepoMap
	Scrape     Scraper
	Platform   PlatformInfo
	// Files is the workspace behind read/ls/glob/grep. It never consults git,
	// so the tools behave the same in a plain directory.
	Files *workspace.Workspace
	// Check is the project's named verification commands. Empty means no
	// check tool is offered.
	Check []config.Check
	// CheckAuto names the checks the harness runs itself at the end of a turn
	// that edited files. Empty means the model is the only thing that runs one.
	CheckAuto []string

	Prompts prompts.Set

	// RecordUsage, when set, receives each turn's accounting at turn end. A
	// callback rather than a writer so the coder keeps knowing nothing about
	// where state lives — the transcript is appended outside it for the same
	// reason. nil in a session that leaves no trace.
	RecordUsage func(TurnUsage)

	// SaveUndo, when set, receives the undo stack and the session's commit
	// hashes whenever either changes. Same shape and same reason as
	// RecordUsage: the coder never learns where state lives, and a session that
	// leaves no trace passes nil.
	SaveUndo func(stack [][]TurnEdit, commits []string, last string)

	// editFormat is the active mode, "tool" or "ask". It starts as the model's
	// EditFormat but /ask and /code switch it at runtime without changing the
	// model, so the tool set and prompt set read this, not Model.EditFormat.
	editFormat string

	// SessionNotes are the session's notes, regenerated from the transcript
	// on demand (--continue at startup, /notes generate mid-session).
	// SessionNotesDate says when they were generated. "" leaves the slot
	// out of the prompt entirely. Notes live in memory only; the transcript
	// is the durable artifact they derive from.
	SessionNotes     string
	SessionNotesDate string

	// Chat state.
	absFnames         []string // ordered, deduped
	absReadOnlyFnames []string
	doneMessages      []llm.Message
	curMessages       []llm.Message
	turnEditedFiles   map[string]bool

	numReflections int // error reflections this turn (maxErrorReflections)
	numSteps       int // work steps this turn (maxSteps)
	autoChecks     int // automatic check rounds this turn (maxAutoCheck)
	// editedSinceCheck gates the automatic checks: they run only when a file
	// has changed since the last time they ran. Without it, a model that
	// answers a failure in prose — "that break was already there and isn't
	// mine" — gets asked the identical question again, because re-running an
	// unchanged tree can only produce the identical output.
	editedSinceCheck bool
	lastSendOutcome  SendOutcome // observability for tests/REPL status
	summaryBackoff   bool        // skip one compaction attempt after a failure

	// Send-scoped buffers.
	partialResponseContent  string
	partialReasoningContent string
	multiResponseContent    string

	// Send-scoped tool-call accumulation, in first-seen
	// index order. toolContinuation makes the next send re-enter on the tool
	// results already appended to curMessages, without adding a user turn.
	partialToolCalls []llm.ToolCall
	toolCallIndex    map[int]int
	toolContinuation bool

	// The message* fields are the *turn's* running totals, accumulated across
	// every send in it and reset by initBeforeMessage. They used to be assigned
	// per send, which made "Cost so far" at the step checkpoint report only the
	// last one — a turn of twelve sends showed the cost of the twelfth.
	messageCost           float64
	totalCost             float64
	costKnown             bool // in-band or priced cost seen this turn
	sessionKnown          bool
	messageTokensSent     int
	messageTokensReceived int
	messageCacheRead      int
	messageCacheWrite     int
	messageEstimated      bool // any send in this turn fell back to an estimate
	messageSends          int
	totalTokensSent       int
	totalTokensReceived   int
	// peakTokensSent is the largest single request this session. The session
	// and turn totals are sums over sends, which is what you are billed but not
	// what fills the window: a five-step turn re-sends its whole conversation
	// each time, so the total can be several times the largest prompt in it.
	// Only the peak says how close the window came to full.
	peakTokensSent  int
	lastUsageReport string // the last send's line; printed only by an aside

	// turnSnap accumulates what this turn has written; pushed onto undoStack at
	// turn end. The stack is the undo substrate that works without git.
	turnSnap  *turnSnapshot
	undoStack []*turnSnapshot

	fence               fence
	commitBeforeMessage []string
	lastCommitHash      string
	sessionCommits      map[string]bool // hashes of this session's auto-commits (/undo gate)
	rejectedUrls        map[string]bool
	turnAutoApprove     map[string]bool // groups auto-approved for this turn
}

type fence struct{ open, close string }

// promptsForFormat picks the prompt set for a mode.
//
// Two remain. "tool" is how Strument works; "ask" is the same tools with the
// mutating ones withheld. The three text formats — diff, diff-fenced, and
// whole — are gone: they existed for models that could not call functions
// reliably, and a model that cannot do that today cannot drive this harness at
// all, since finding and reading files are tool calls too.
func promptsForFormat(format string) prompts.Set {
	if format == "ask" {
		return prompts.Ask
	}
	return prompts.Tool
}

// PlatformInfo feeds the {platform} prompt slot deterministically
// (injectable for fixtures).
type PlatformInfo struct {
	Platform string // e.g. "Linux-6.1-x86_64"
	ShellVar string
	ShellVal string
	Language string // human-readable, e.g. "English"; "" => none
	Date     string // YYYY-MM-DD
	WorkDir  string // absolute path to the project root
	InGit    bool
}

// New builds a Coder with aider-like defaults for script mode.
func New(root string, model *config.Model) *Coder {
	c := &Coder{
		Root:                 root,
		Model:                model,
		SuggestShellCommands: true,
		Stream:               true,
		PrefillSupported:     true,
		UseSystemPrompt:      true,
		MaxSteps:             25,
		MaxErrorReflections:  3,
		Tokens:               RuneCounter{},
		Clock:                RealClock{},
		Out:                  &StdOutput{},
		Prompts:              promptsForFormat(model.EditFormat),
		editFormat:           model.EditFormat,
		Files:                workspace.New(root),
		rejectedUrls:         map[string]bool{},
		turnEditedFiles:      map[string]bool{},
		turnAutoApprove:      map[string]bool{},
	}
	c.Platform = defaultPlatformInfo(c)
	// The observation tools are contained to the project root, with the same
	// exemption edits get: a file the user pinned is sanctioned wherever it
	// lives. A predicate rather than a snapshot, so /add and /drop need no
	// bookkeeping and the two lists cannot go stale.
	c.Files.Pinned = c.isPinned
	return c
}

// isPinned reports whether abs is a file the user added with /add or
// /read-only. Out-of-tree references are the case it exists for: /read-only is
// the only channel for a file outside the project, and a model that has its
// contents in context will sometimes read it to check.
func (c *Coder) isPinned(abs string) bool {
	return slices.Contains(c.absFnames, abs) || slices.Contains(c.absReadOnlyFnames, abs)
}

func defaultPlatformInfo(c *Coder) PlatformInfo {
	shellVar := "SHELL"
	if os.PathSeparator == '\\' {
		shellVar = "COMSPEC"
	}
	return PlatformInfo{
		Platform: fmt.Sprintf("%s-%s", osName(), archName()),
		ShellVar: shellVar,
		ShellVal: os.Getenv(shellVar),
		Language: detectUserLanguage(c.ChatLanguage),
		Date:     time.Now().Format("2006-01-02"),
		WorkDir:  c.Root,
	}
}

// AddFile adds an editable file to the chat by absolute or root-relative
// path.
func (c *Coder) AddFile(path string) {
	abs := c.absRootPath(path)
	if !slices.Contains(c.absFnames, abs) {
		c.absFnames = append(c.absFnames, abs)
	}
}

// AddReadOnlyFile adds a read-only reference file.
func (c *Coder) AddReadOnlyFile(path string) {
	abs := c.absRootPath(path)
	if !slices.Contains(c.absReadOnlyFnames, abs) {
		c.absReadOnlyFnames = append(c.absReadOnlyFnames, abs)
	}
}

func (c *Coder) absRootPath(path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.Root, path)
	}
	return resolvePath(filepath.Clean(abs))
}

func (c *Coder) relFname(abs string) string {
	rel, err := filepath.Rel(resolvePath(c.Root), abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// resolvePath returns abs with symlinks resolved, so a path that reaches the
// repo through a symlinked directory — a symlinked checkout, or an arg the CLI
// made absolute in the symlink namespace — shares the git-resolved root's
// namespace and stays repo-relative. It resolves the deepest existing ancestor
// and re-appends the not-yet-created tail, so create_file targets resolve too.
// On failure it returns abs unchanged.
func resolvePath(abs string) string {
	rest := ""
	dir := abs
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs // reached the root without resolving anything
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

func (c *Coder) inchatRelativeFiles() []string {
	set := map[string]bool{}
	for _, f := range c.absFnames {
		set[c.relFname(f)] = true
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// initBeforeMessage resets per-top-level-message state.
func (c *Coder) initBeforeMessage() {
	c.turnEditedFiles = map[string]bool{}
	c.turnAutoApprove = map[string]bool{}
	c.numReflections = 0
	c.numSteps = 0
	c.autoChecks = 0
	c.editedSinceCheck = false
	c.turnSnap = nil
	c.messageCost = 0
	c.costKnown = false
	c.messageTokensSent = 0
	c.messageTokensReceived = 0
	c.messageCacheRead = 0
	c.messageCacheWrite = 0
	c.messageEstimated = false
	c.messageSends = 0
	if c.Repo != nil {
		c.commitBeforeMessage = append(c.commitBeforeMessage, c.Repo.HeadSHA())
	}
}

// confirmTurn wraps c.Confirm with turn-scoped auto-approve. If the user
// answered "a" (always this turn) to a previous Confirm with the same Group,
// this one is approved without prompting. The first "a" answer records the
// group and returns true. Callers that don't need turn-scoping —
// confirmMoreSteps, checkTokens — call c.Confirm.Confirm directly.
func (c *Coder) confirmTurn(req ConfirmRequest) bool {
	if req.Group != "" && c.turnAutoApprove[req.Group] {
		return true
	}
	res := c.Confirm.Confirm(req)
	if res.AlwaysThisTurn && req.Group != "" {
		c.turnAutoApprove[req.Group] = true
	}
	return res.Yes || res.AlwaysThisTurn
}

// Run executes one scripted message (script mode) and returns the
// last send's content regardless of outcome.
func (c *Coder) Run(ctx context.Context, withMessage string) string {
	c.runOne(ctx, withMessage, true)
	return c.multiResponseContent + c.partialResponseContent
}

// runOne is the turn loop. It keeps sending while the model has something to
// react to — a tool result it hasn't seen (OutcomeContinue) or a mistake to fix
// (OutcomeReflect) — and stops when the model has nothing left to say, the
// human interrupts, or a budget is spent. The turn boundary is still the
// human's: nothing here starts new work of its own.
func (c *Coder) runOne(ctx context.Context, userMessage string, preproc bool) {
	c.initBeforeMessage()

	message := userMessage
	if preproc {
		message = c.preprocUserInput(ctx, userMessage)
	}
	if message == "" {
		return
	}

	// Committing and rotating settled history are both turn-end concerns.
	// Mid-loop the tool results must stay in cur, and summarizing them away
	// between steps would compact the very results the next send reacts to;
	// committing mid-loop would spend one commit per send on fragments of one
	// change. A defer covers every exit — budget declined, reflection cap,
	// interrupted, or done. An interrupted turn's edits are real, so they are
	// committed too.
	defer func() {
		c.commitTurn()
		c.pushTurnSnapshot()
		c.endTurnHistory()
		// Last, so the accounting closes the turn: the commit line above it, and
		// nothing under it. This is what the per-send reorder was reaching for,
		// now at the scope where a reader actually wants the number.
		c.flushTurnUsage()
	}()

	// Outcome-driven so a re-send that carries no message text — both a tool
	// reflection and a tool continuation re-enter on the appended tool results
	// — keeps the loop going.
	for {
		outcome, reflection := c.sendMessage(ctx, message)
		c.lastSendOutcome = outcome

		switch outcome {
		case OutcomeReflect:
			if c.numReflections >= c.MaxErrorReflections {
				c.Out.Warningf("Only %d reflections allowed, stopping.", c.MaxErrorReflections)
				return
			}
			c.numReflections++
			message = reflection

		case OutcomeContinue:
			c.numSteps++
			if c.numSteps >= c.MaxSteps && !c.confirmMoreSteps() {
				return
			}
			message = "" // the tool results are the message

		case OutcomeSuccess:
			// The model has nothing more to call, so it believes it is done.
			// That is the moment to check, if the project asked us to.
			report, ok := c.runAutoCheck(ctx)
			if !ok {
				return
			}
			message = report

		default:
			return
		}
	}
}

// runAutoCheck runs the project's check_auto checks at the end of a turn that
// edited files, and reports whether the turn should continue.
//
// It fires here rather than after each edit because a mid-turn state is
// legitimately broken: edit one file, then its caller, and in between nothing
// compiles. Checking there would spend budget on failures the model was already
// about to fix. At turn end the model has declared itself finished, which is
// exactly when an independent check is worth something — and the reason to make
// it independent is that the model's judgement about *which* check mattered is
// the part that fails. A run that passed the tests and never linted still
// reports "the checks pass".
//
// The returned message is a user turn, not a tool result, which is a deliberate
// exception to the rule that reflection is a tool error. There is no call to
// answer here: the model did not ask for this, the harness is speaking
// unprompted, and a user message is the honest shape for that.
func (c *Coder) runAutoCheck(ctx context.Context) (message string, keepGoing bool) {
	// Nothing changed since the last run — either the turn edited nothing at
	// all, or the model replied to a failure without touching a file, which is
	// a considered answer ("that isn't mine") and not something to re-ask.
	if len(c.CheckAuto) == 0 || !c.editedSinceCheck {
		return "", false
	}
	c.editedSinceCheck = false
	if c.autoChecks >= maxAutoCheck {
		c.Out.Warningf("The automatic checks have run %d times without passing; stopping here.", maxAutoCheck)
		return "", false
	}
	c.autoChecks++

	c.Out.Toolf("Running the automatic checks.")
	transcript, passed := c.runChecks(ctx, c.CheckAuto)
	if passed {
		return "", false
	}
	// The wording matters, and live testing is what showed why. "did not pass
	// after your changes" reads as "you caused this", which puts the model
	// between this message and the standing instruction to leave unrelated code
	// alone. Observed: it declined to fix a pre-existing failure, was asked
	// again by the next round, and gave in — two rounds spent on an argument the
	// harness started. Saying plainly that reporting is an acceptable answer
	// settles it in one.
	return "The automatic checks ran after your changes and did not pass:\n\n" + transcript +
		"\nIf this is something you broke, fix it. If it was already failing and is unrelated to " +
		"what you changed, say so and stop — don't fix it unless the user asks.", true
}

// confirmMoreSteps is the budget checkpoint. A long turn is legitimate, but it
// should not run away unnoticed, so the user is shown what it has done so far
// and asked whether to keep going. Declining ends the turn normally, with the
// work up to that point already applied and committed. Answering yes buys
// another MaxSteps.
func (c *Coder) confirmMoreSteps() bool {
	files := len(c.turnEditedFiles)
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	c.Out.Printf("This turn has run %d steps and edited %d %s.", c.numSteps, files, noun)
	if c.costKnown {
		c.Out.Printf("Cost so far: $%s.", formatCost(c.messageCost))
	}

	res := c.Confirm.Confirm(ConfirmRequest{Prompt: "Keep going?"})
	if !res.Yes {
		c.Out.Printf("Stopping here. The work so far is applied; say what to do next.")
		return false
	}
	c.numSteps = 0
	c.autoChecks = 0
	c.editedSinceCheck = false
	return true
}

// preprocUserInput handles URLs.
// Slash commands are dispatched by the REPL layer before this.
func (c *Coder) preprocUserInput(ctx context.Context, inp string) string {
	if inp == "" {
		return ""
	}
	return c.checkForUrls(ctx, inp)
}

// StdOutput writes to stdout/stderr.
type StdOutput struct {
	// Thinking is how much of the model's reasoning to show. The zero value
	// shows all of it.
	Thinking render.ThinkingDisplay

	diffs     *render.ToolDiffSet
	wroteText bool
	think     *render.Thinking
	// streamed records that this send drew something, so the flush closes it
	// with a blank line. sep is the gap before the next block of thinking. Both
	// mirror repl/output.go exactly; the two outputs lay a turn out the same
	// way and differ only in the color the terminal adds.
	streamed bool
	sep      render.GroupSep
}

func (o *StdOutput) Printf(format string, args ...any) {
	fmt.Print(render.Sanitize(fmt.Sprintf(format, args...)) + "\n")
	o.sep.Clear() // the harness's own voice, outside any step
}
func (o *StdOutput) Toolf(format string, args ...any) {
	fmt.Print(render.Sanitize(fmt.Sprintf(format, args...)) + "\n") // no color outside the REPL
	o.sep.Drew()
}
func (o *StdOutput) Warningf(format string, args ...any) {
	fmt.Fprint(os.Stderr, render.Sanitize(fmt.Sprintf(format, args...))+"\n")
}
func (o *StdOutput) Errorf(format string, args ...any) {
	fmt.Fprint(os.Stderr, render.Sanitize(fmt.Sprintf(format, args...))+"\n")
}
func (o *StdOutput) StreamText(delta string) {
	// The one separator that still follows the thinking rather than preceding
	// it; repl/output.go's separateFromAnswer says why.
	if o.endReasoning() {
		fmt.Println()
		o.sep.Clear()
	}
	if delta != "" {
		o.wroteText = true
		o.streamed = true
		o.sep.Drew()
	}
	fmt.Print(render.Sanitize(delta))
}

// StreamReasoning renders the model's thinking, plain.
//
// It used to be an empty function, so `strument -m … > transcript.txt` dropped
// the whole reasoning trace without saying so, while the same session run
// interactively showed it. Which mode you happened to use decided whether the
// thinking existed — a decision nobody made, and one that cost several live
// runs chasing a marker that could not appear. How much to show is now
// reasoning_display's to answer, in both modes alike.
func (o *StdOutput) StreamReasoning(delta string) {
	if o.think == nil {
		t := render.PlainThinking(os.Stdout, o.Thinking)
		// Pay the group separator at the opening marker: thinking opens lazily,
		// so a block that turns out to be empty never reaches Marker, and this
		// is the last point still above the "‹thinking›" the gap must precede.
		inner := t.Marker
		t.Marker = func(s string) {
			o.sep.Before(os.Stdout)
			o.streamed = true
			inner(s)
		}
		o.think = t
	}
	o.think.Write(render.Sanitize(delta))
}

// endReasoning closes an open thinking block and reports whether there was one.
func (o *StdOutput) endReasoning() bool {
	if o.think == nil {
		return false
	}
	rendered := o.think.End()
	o.think = nil
	return rendered
}

func (o *StdOutput) StreamToolCall(index int, name, args string) {
	// Reasoning gave way to the calls it was about: no separator, since the
	// thinking heads this group. Clearing streamed by hand keeps the flush from
	// putting a blank directly above an outcome line printed by Toolf.
	if o.endReasoning() {
		o.streamed = false
	}
	if o.diffs == nil {
		// Break to a fresh line so the first diff header isn't glued to the
		// answer text (which need not end in a newline).
		if o.wroteText {
			fmt.Println()
			o.streamed = false // that blank separates; the flush need not repeat it
		}
		o.diffs = render.NewToolDiffSet(os.Stdout, false, render.DefaultTheme())
	}
	o.diffs.Write(index, name, args)
}

func (o *StdOutput) FlushStream() {
	o.endReasoning() // a send that was nothing but thinking still closes it
	if o.diffs != nil {
		o.diffs.Flush()
		if o.diffs.Drew() {
			o.streamed = true
		}
		o.diffs = nil
	}
	o.wroteText = false
	// Only when this send drew. A send whose tool calls all render nothing — a
	// bash command, or any observation tool — prints its outcome through Toolf
	// after this runs, so a blank here would land above that outcome rather
	// than after it. The gap before the next step is GroupSep's to write.
	if o.streamed {
		fmt.Println()
		o.streamed = false
		o.sep.Clear()
	}
}

// ThinkingDisplay translates the config's answer into the renderer's. The two
// types are deliberately separate: render must not import config, and this is
// the whole of what it needs to know.
func ThinkingDisplay(d config.ReasoningDisplay) render.ThinkingDisplay {
	switch d.Mode {
	case config.ReasoningOff:
		return render.ThinkingDisplay{Mode: render.ThinkingOff}
	case config.ReasoningCapped:
		return render.ThinkingDisplay{Mode: render.ThinkingCapped, Lines: d.Lines}
	default:
		return render.ThinkingDisplay{Mode: render.ThinkingFull}
	}
}

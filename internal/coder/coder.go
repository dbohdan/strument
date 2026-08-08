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
	maxErrorReflections = 3
	// maxSteps bounds the work rounds in one turn. It is a checkpoint, not a
	// wall: on exhaustion the user is shown what the turn has done so far and
	// asked whether to keep going.
	maxSteps = 25
	// maxAutoVerify bounds the rounds the harness itself starts by running the
	// project's checks. It is small and separate on purpose: a model caught in a
	// fix-break cycle should hand back to the human rather than spend the work
	// budget on rounds nobody asked for.
	maxAutoVerify = 3
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
	ReminderPlacement    string // "sys" | "user" (aider model default: "user")
	PrefillSupported     bool   // continuation on finish_reason=length
	ExamplesAsSysMsg     bool
	UseSystemPrompt      bool
	SystemPromptPrefix   string
	ChatLanguage         string

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
	// Verify is the project's named verification commands. Empty means no
	// verify tool is offered.
	Verify []config.VerifyCheck
	// VerifyAuto names the checks the harness runs itself at the end of a turn
	// that edited files. Empty means the model is the only thing that runs one.
	VerifyAuto []string

	Prompts prompts.Set

	// editFormat is the active mode, "tool" or "ask". It starts as the model's
	// EditFormat but /ask and /code switch it at runtime without changing the
	// model, so the tool set and prompt set read this, not Model.EditFormat.
	editFormat string

	// Chat state.
	absFnames         []string // ordered, deduped
	absReadOnlyFnames []string
	doneMessages      []llm.Message
	curMessages       []llm.Message
	turnEditedFiles   map[string]bool

	numReflections int // error reflections this turn (maxErrorReflections)
	numSteps       int // work steps this turn (maxSteps)
	autoVerifies   int // automatic check rounds this turn (maxAutoVerify)
	// editedSinceVerify gates the automatic checks: they run only when a file
	// has changed since the last time they ran. Without it, a model that
	// answers a failure in prose — "that break was already there and isn't
	// mine" — gets asked the identical question again, because re-running an
	// unchanged tree can only produce the identical output.
	editedSinceVerify bool
	lastSendOutcome   SendOutcome // observability for tests/REPL status

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

	messageCost           float64
	totalCost             float64
	costKnown             bool // in-band or priced cost seen this message
	sessionKnown          bool
	messageTokensSent     int
	messageTokensReceived int
	totalTokensSent       int
	totalTokensReceived   int
	lastUsageReport       string

	// turnSnap accumulates what this turn has written; pushed onto undoStack at
	// turn end. The stack is the undo substrate that works without git.
	turnSnap  *turnSnapshot
	undoStack []*turnSnapshot

	fence               fence
	commitBeforeMessage []string
	lastCommitHash      string
	sessionCommits      map[string]bool // hashes of this session's auto-commits (/undo gate)
	ignoreMentions      map[string]bool
	rejectedUrls        map[string]bool

	// Frozen repo map (caching only): computed once per chat-file set and reused
	// until the set changes, so the cached prompt prefix stays byte-stable.
	cachedRepoMap    string
	cachedRepoMapKey string
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
	InGit    bool
}

// New builds a Coder with aider-like defaults for script mode.
func New(root string, model *config.Model) *Coder {
	c := &Coder{
		Root:                 root,
		Model:                model,
		SuggestShellCommands: true,
		Stream:               true,
		ReminderPlacement:    "user",
		PrefillSupported:     true,
		UseSystemPrompt:      true,
		Tokens:               RuneCounter{},
		Clock:                RealClock{},
		Out:                  &StdOutput{},
		Prompts:              promptsForFormat(model.EditFormat),
		editFormat:           model.EditFormat,
		Files:                workspace.New(root),
		ignoreMentions:       map[string]bool{},
		rejectedUrls:         map[string]bool{},
		turnEditedFiles:      map[string]bool{},
	}
	c.Platform = defaultPlatformInfo(c)
	return c
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

// allRelativeFiles is the repo's tracked files, or just the chat files
// without git.
func (c *Coder) allRelativeFiles() []string {
	var files []string
	if c.Repo != nil {
		files = c.Repo.TrackedFiles()
	} else {
		files = c.inchatRelativeFiles()
	}
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func (c *Coder) addableRelativeFiles() []string {
	exclude := map[string]bool{}
	for _, f := range c.inchatRelativeFiles() {
		exclude[f] = true
	}
	for _, f := range c.absReadOnlyFnames {
		exclude[c.relFname(f)] = true
	}
	var out []string
	for _, f := range c.allRelativeFiles() {
		if !exclude[f] {
			out = append(out, f)
		}
	}
	return out
}

// initBeforeMessage resets per-top-level-message state.
func (c *Coder) initBeforeMessage() {
	c.turnEditedFiles = map[string]bool{}
	c.numReflections = 0
	c.numSteps = 0
	c.autoVerifies = 0
	c.editedSinceVerify = false
	c.turnSnap = nil
	c.messageCost = 0
	c.costKnown = false
	if c.Repo != nil {
		c.commitBeforeMessage = append(c.commitBeforeMessage, c.Repo.HeadSHA())
	}
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
		if c.editFormat == "tool" && len(c.turnEditedFiles) > 0 {
			c.moveBackCurMessages()
		}
	}()

	// Outcome-driven so a re-send that carries no message text — both a tool
	// reflection and a tool continuation re-enter on the appended tool results
	// — keeps the loop going.
	for {
		outcome, reflection := c.sendMessage(ctx, message)
		c.lastSendOutcome = outcome

		switch outcome {
		case OutcomeReflect:
			if c.numReflections >= maxErrorReflections {
				c.Out.Warningf("Only %d reflections allowed, stopping.", maxErrorReflections)
				return
			}
			c.numReflections++
			message = reflection

		case OutcomeContinue:
			c.numSteps++
			if c.numSteps >= maxSteps && !c.confirmMoreSteps() {
				return
			}
			message = "" // the tool results are the message

		case OutcomeSuccess:
			// The model has nothing more to call, so it believes it is done.
			// That is the moment to check, if the project asked us to.
			report, ok := c.runAutoVerify(ctx)
			if !ok {
				return
			}
			message = report

		default:
			return
		}
	}
}

// runAutoVerify runs the project's verify_auto checks at the end of a turn that
// edited files, and reports whether the turn should continue.
//
// It fires here rather than after each edit because a mid-turn state is
// legitimately broken: edit one file, then its caller, and in between nothing
// compiles. Verifying there would spend budget on failures the model was already
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
func (c *Coder) runAutoVerify(ctx context.Context) (message string, keepGoing bool) {
	// Nothing changed since the last run — either the turn edited nothing at
	// all, or the model replied to a failure without touching a file, which is
	// a considered answer ("that isn't mine") and not something to re-ask.
	if len(c.VerifyAuto) == 0 || !c.editedSinceVerify {
		return "", false
	}
	c.editedSinceVerify = false
	if c.autoVerifies >= maxAutoVerify {
		c.Out.Warningf("The automatic checks have run %d times without passing; stopping here.", maxAutoVerify)
		return "", false
	}
	c.autoVerifies++

	c.Out.Toolf("Running the automatic checks.")
	transcript, passed := c.runChecks(ctx, c.VerifyAuto)
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
// another maxSteps.
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

	yes, _ := c.Confirm.Confirm(ConfirmRequest{Prompt: "Keep going?"})
	if !yes {
		c.Out.Printf("Stopping here. The work so far is applied; say what to do next.")
		return false
	}
	c.numSteps = 0
	c.autoVerifies = 0
	c.editedSinceVerify = false
	return true
}

// preprocUserInput handles empty input, file mentions, and URLs.
// Slash commands are dispatched by the REPL layer before this.
func (c *Coder) preprocUserInput(ctx context.Context, inp string) string {
	if inp == "" {
		return ""
	}
	c.checkForFileMentions(inp)
	inp = c.checkForUrls(ctx, inp)
	return inp
}

// StdOutput writes to stdout/stderr.
type StdOutput struct {
	diffs     *render.ToolDiffSet
	wroteText bool
}

func (o *StdOutput) Printf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}
func (o *StdOutput) Toolf(format string, args ...any) {
	fmt.Printf(format+"\n", args...) // no color outside the REPL
}
func (o *StdOutput) Warningf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
func (o *StdOutput) Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
func (o *StdOutput) StreamText(delta string) {
	if delta != "" {
		o.wroteText = true
	}
	fmt.Print(delta)
}
func (o *StdOutput) StreamReasoning(_ string) {}

func (o *StdOutput) StreamToolCall(index int, name, args string) {
	if o.diffs == nil {
		// Break to a fresh line so the first diff header isn't glued to the
		// answer text (which need not end in a newline).
		if o.wroteText {
			fmt.Println()
		}
		o.diffs = render.NewToolDiffSet(os.Stdout, false, render.DefaultTheme())
	}
	o.diffs.Write(index, name, args)
}

func (o *StdOutput) FlushStream() {
	if o.diffs != nil {
		o.diffs.Flush()
		o.diffs = nil
	}
	o.wroteText = false
	fmt.Println()
}

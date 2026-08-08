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

	Prompts prompts.Set

	// editFormat is the active format ("diff"/"diff-fenced"/"whole"/"ask").
	// It starts as the model's EditFormat but /ask and /code switch it at
	// runtime without changing the model, so the apply dispatch and prompt
	// set read this, not Model.EditFormat.
	editFormat string

	// Chat state.
	absFnames         []string // ordered, deduped
	absReadOnlyFnames []string
	doneMessages      []llm.Message
	curMessages       []llm.Message
	turnEditedFiles   map[string]bool

	numReflections  int         // error reflections this turn (maxErrorReflections)
	numSteps        int         // work steps this turn (maxSteps)
	lastSendOutcome SendOutcome // observability for tests/REPL status

	// Send-scoped buffers.
	partialResponseContent  string
	partialReasoningContent string
	multiResponseContent    string

	// Send-scoped tool-call accumulation ("tool" edit format), in first-seen
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

	// Rotating settled history is a turn-end concern for the tool format:
	// mid-loop the tool results must stay in cur, and summarizing them away
	// between steps would compact the very results the next send reacts to.
	// A defer covers every exit — budget declined, reflection cap, or done.
	defer func() {
		if c.editFormat == "tool" && len(c.turnEditedFiles) > 0 {
			c.moveBackCurMessages("")
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

		default:
			return
		}
	}
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

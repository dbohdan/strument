package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/dbohdan/strument/internal/config"
	"github.com/dbohdan/strument/internal/llm"
	"github.com/dbohdan/strument/internal/prompts"
	"github.com/dbohdan/strument/internal/repomap"
)

const maxReflections = 3

// Coder is the chat loop state (basecoder-spec §0).
type Coder struct {
	Root  string
	Model *config.Model

	// Options.
	DryRun               bool
	AutoCommits          bool
	CacheHeaders         bool
	SuggestShellCommands bool // false gates execution too (§6.4)
	Stream               bool
	ReminderPlacement    string // "sys" | "user" (aider model default: "user")
	PrefillSupported     bool   // continuation on finish_reason=length (§2.1)
	ExamplesAsSysMsg     bool
	UseSystemPrompt      bool
	SystemPromptPrefix   string
	ChatLanguage         string

	// Ports.
	Client   llm.ModelClient
	Tokens   TokenCounter
	Confirm  Confirmer
	Runner   CommandRunner
	Repo     Repo
	Clock    Clock
	Out      Output
	RepoMap  *repomap.RepoMap
	Scrape   Scraper
	Platform PlatformInfo

	Prompts prompts.Set

	// Chat state (§0).
	absFnames         []string // ordered, deduped
	absReadOnlyFnames []string
	doneMessages      []llm.Message
	curMessages       []llm.Message
	shellCommands     []string // response order, dedup by first occurrence; reset in initBeforeMessage
	turnEditedFiles   map[string]bool

	numReflections  int
	lastSendOutcome SendOutcome // observability for tests/REPL status

	// Send-scoped buffers (lifecycles in §2).
	partialResponseContent  string
	partialReasoningContent string
	multiResponseContent    string

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
}

type fence struct{ open, close string }

// promptsForFormat picks the prompt set for an edit format.
func promptsForFormat(format string) prompts.Set {
	switch format {
	case "whole":
		return prompts.WholeFile
	case "diff-fenced":
		return prompts.EditBlockFenced
	default:
		return prompts.EditBlock
	}
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
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(c.Root, path))
}

func (c *Coder) relFname(abs string) string {
	rel, err := filepath.Rel(c.Root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
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

// initBeforeMessage resets per-top-level-message state (§1.3).
func (c *Coder) initBeforeMessage() {
	c.turnEditedFiles = map[string]bool{}
	c.numReflections = 0
	c.shellCommands = nil
	c.messageCost = 0
	c.costKnown = false
	if c.Repo != nil {
		c.commitBeforeMessage = append(c.commitBeforeMessage, c.Repo.HeadSHA())
	}
}

// Run executes one scripted message (script mode, §1.1) and returns the
// last send's content regardless of outcome.
func (c *Coder) Run(ctx context.Context, withMessage string) string {
	c.runOne(ctx, withMessage, true)
	return c.multiResponseContent + c.partialResponseContent
}

// runOne is the reflection loop (§1.3): up to 4 sends (initial + 3
// follow-ups).
func (c *Coder) runOne(ctx context.Context, userMessage string, preproc bool) {
	c.initBeforeMessage()

	message := userMessage
	if preproc {
		message = c.preprocUserInput(ctx, userMessage)
	}

	for message != "" {
		outcome, reflection := c.sendMessage(ctx, message)
		c.lastSendOutcome = outcome
		if outcome != OutcomeReflect {
			break
		}
		if c.numReflections >= maxReflections {
			c.Out.Warningf("Only %d reflections allowed, stopping.", maxReflections)
			return
		}
		c.numReflections++
		message = reflection
	}
}

// preprocUserInput handles empty input, file mentions, and URLs (§1.4).
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
type StdOutput struct{}

func (o *StdOutput) Printf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}
func (o *StdOutput) Warningf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
func (o *StdOutput) Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
func (o *StdOutput) StreamText(delta string)  { fmt.Print(delta) }
func (o *StdOutput) StreamReasoning(_ string) {}
func (o *StdOutput) FlushStream()             { fmt.Println() }

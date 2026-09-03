package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repomap"
	"dbohdan.com/strument/internal/skill"
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
	SystemPromptPrefix   string
	ChatLanguage         string
	// OfferCode, when false, withholds the run_code tool from the schema and
	// empties the {code_tools} prompt slot. Default true, set in New; the
	// false case is the feature-reverted arm of the code-mode trial and a
	// future user setting. Keeping the condition in one field is what lets
	// the prompt's tool list track the schema instead of drifting from it.
	OfferCode bool
	// ObservationViaRunCode, when true, withholds the direct read-only tools
	// (read, grep, glob, ls, symbol) from the schema and forces OfferCode on:
	// all observation goes through code programs calling the bridged tools.
	// This is the force arm of the code-uptake trials — the complement of
	// OfferCode=false. Rather than persuading the model to prefer `run_code`, it
	// removes the competing tools, the closed-world condition the 2026-08
	// trial measured from the other side. A direct call the model makes anyway
	// is answered with a redirect to code, not silently dropped — see
	// redirectNote. Default false; set from config `observation_via_run_code`.
	// Off in this mode are only the observation tools: edit, bash, check,
	// commit, webfetch, and ask_user_question are unchanged, and webfetch
	// stays direct because the bridge cannot carry it.
	ObservationViaRunCode bool

	// MaxSteps is the work-step budget per turn — a checkpoint, not a wall.
	// On exhaustion the user is shown what the turn has done and asked
	// whether to keep going. Configurable; the default (25) is set by New.
	MaxSteps int
	// MaxErrorReflections is the error-reflection budget per turn. An error
	// reflection is the model recovering from its own mistake and should
	// stay rare. Configurable; the default (3) is set by New.
	MaxErrorReflections int
	// WebfetchAllow are origins (host:port) the webfetch tool may fetch without
	// asking. From `webfetch_allow`; see fetchAllowed for what it does and does
	// not promise.
	WebfetchAllow []string
	// ScrapeRunsCommand records that Scrape spawns the configured `scraper`
	// command rather than fetching in process, which is what decides whether a
	// fetch is gated by the sandbox.
	ScrapeRunsCommand bool

	// LoopDetection stops a reply that has degenerated into repeating one
	// phrase over and over. On by default; see loopdetect.go for what counts.
	LoopDetection bool

	// Ports.
	Client     llm.ModelClient
	Summarizer *ChatSummary // nil => chat-history summarization disabled
	Tokens     TokenCounter
	Confirm    Confirmer
	// Asker answers ask_user_question calls. nil (script mode, tests) means
	// no interactive terminal: the call is answered with an error result
	// rather than hanging, the same convention as a nil Repo.
	Asker   Asker
	Runner  CommandRunner
	Repo    Repo
	Clock   Clock
	Out     Output
	RepoMap *repomap.RepoMap
	Scrape  Scraper
	// Search is nil unless the user configured a backend, and that nil is what
	// decides whether the tool is offered at all.
	Search Searcher
	// Skills are what discovery found, trusted and not. Only the trusted ones
	// are ever offered to the model — skill.Usable is the filter, and every
	// path putting skill text in front of a model goes through it. The
	// untrusted ones are kept so the session can tell the *user* they exist.
	Skills   []skill.Skill
	Platform PlatformInfo
	// editsExact and editsFuzzy tally how this turn's edits found their text.
	editsExact int
	editsFuzzy int

	// shown records the version of each file the model was last shown, so an
	// edit to a file that moved underneath is refused rather than applied to
	// content the model never saw. See staleness.go.
	shown *shownFiles

	// Files is the workspace behind read/ls/glob/grep. It never consults git,
	// so the tools behave the same in a plain directory.
	Files *workspace.Workspace
	// Check is the project's named verification commands. Empty means no
	// check tool is offered.
	Check []config.Check
	// CheckAuto names the checks the harness runs itself at the end of a turn
	// that edited files. Empty means the model is the only thing that runs one.
	CheckAuto []string
	// Sandbox is what the harness knows about its own confinement. It gates
	// model-caused execution and shapes the message shown when a command is
	// denied; applying the sandbox happens once, in main, before any model
	// interaction.
	Sandbox SandboxState

	// ShellTimeout bounds one model-caused command. Zero takes
	// defaultShellTimeout; negative means no deadline. /run is never bounded.
	ShellTimeout time.Duration

	// Examples are config-provided few-shot messages (example_messages),
	// appended to the active prompt set's examples on every format switch.
	// nil in a session without them. See SetEditFormat.
	Examples []config.ExampleMessage

	// EnvAllow extends the default environment allowlist (envallow.go) with
	// names from the config's `env_allow`. It applies to every command the
	// model caused to run — bash, checks, the scraper — never to /run.
	EnvAllow []string

	Prompts prompts.Set

	// RecordUsage, when set, receives each turn's accounting at turn end. A
	// callback rather than a writer so the coder keeps knowing nothing about
	// where state lives — the transcript is appended outside it for the same
	// reason. nil in a session that leaves no trace.
	RecordUsage func(TurnUsage)

	// OnCrash receives the turn's partial answer when a turn dies with a panic:
	// Run recovers, calls this, and re-panics so the process still dies. The
	// transcript writer lives outside the Coder, so this is how a turn that
	// never returned still reaches it — with the work it did along the way,
	// which is otherwise lost with the process. nil (the default) records
	// nothing; a panic then behaves exactly as before.
	//
	// Only Run calls it. An aside and the internal commands are single sends
	// with no work to lose, and their panics are not worth a transcript entry.
	OnCrash func(partialAnswer string)

	// SaveUndo, when set, receives the undo stack and the session's commit
	// hashes whenever either changes. Same shape and same reason as
	// RecordUsage: the coder never learns where state lives, and a session that
	// leaves no trace passes nil.
	SaveUndo func(stack [][]TurnEdit, commits []string, last string)

	// Recorder receives the JSONL log's records when one is wired; nil is the
	// default and costs nothing. recordedMessages is its watermark into
	// curMessages — see record.go for why the flush is per-send.
	Recorder         Recorder
	recordedMessages int

	// editFormat is the active mode, "tool" or "ask". It starts as the model's
	// EditFormat but /ask and /code switch it at runtime without changing the
	// model, so the tool set and prompt set read this, not Model.EditFormat.
	editFormat string

	// SessionNotes are the session's notes, generated from the transcript
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
	// toolLog tees Toolf into a per-turn record, so the transcript can say what
	// the turn did and not only what it said about it. Installed lazily by
	// recordToolLines; see toollog.go.
	toolLog *toolLog

	numReflections int // error reflections this turn (maxErrorReflections)
	numSteps       int // work steps this turn (maxSteps)
	autoChecks     int // automatic check rounds this turn (maxAutoCheck)
	// toolLoops counts this turn's read-class tool calls for exact repetition.
	// Per turn: a new task may legitimately re-read what the last one read.
	toolLoops *toolLoopWatcher
	// editedSinceCheck gates the automatic checks: they run only when a file
	// has changed since the last time they ran. Without it, a model that
	// answers a failure in prose — "that break was already there and isn't
	// mine" — gets asked the identical question again, because re-running an
	// unchanged tree can only produce the identical output.
	editedSinceCheck bool
	lastSendOutcome  SendOutcome // observability for tests/REPL status
	summaryBackoff   bool        // skip one compaction attempt after a failure

	// cancelSend stops the send in flight, and only that send. See
	// sendSteerable and InterruptSend. Guarded because the caller is a signal
	// handler on another goroutine.
	sendMu     sync.Mutex
	cancelSend context.CancelFunc

	// Send-scoped buffers.
	partialResponseContent  string
	partialReasoningContent string
	multiResponseContent    string

	// turnHistory accumulates content from interrupted sends so the
	// transcript records the full arc of a steered turn — what the model
	// was saying when it was stopped, the steer, and the final answer —
	// rather than only the last send's output. Reset at the start of
	// each turn; accumulated in runOne between sends.
	turnHistory string

	// Send-scoped tool-call accumulation, in first-seen index order.
	partialToolCalls []llm.ToolCall
	toolCallIndex    map[int]int

	// resumeInPlace makes the next send re-enter on what is already in
	// curMessages, without adding a user turn for it.
	//
	// Two things resume that way, which is why the flag is not named after
	// either. A tool continuation re-enters on the tool results just appended.
	// An interrupted turn the user chose to continue re-enters on the partial
	// reply and the note explaining it was cut off. Both have the message
	// already; appending an empty user turn for them would put an empty message
	// on the wire and cost a turn's worth of confusion.
	resumeInPlace bool

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
	// side* is the current session-notes request. It is separate from the
	// turn totals because notes can be generated between turns.
	sideCost           float64
	sideCostKnown      bool
	sideTokensSent     int
	sideTokensReceived int
	sideCacheRead      int
	sideCacheWrite     int
	sideUsageRecorded  bool
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
	turnAutoApprove     map[string]bool // groups auto-approved for this turn
	// sessionAutoApprove is the same thing outliving the turn, and
	// initBeforeMessage deliberately does not clear it. Only webfetch writes
	// here; see ConfirmRequest.GroupSession for why the shell gate does not.
	// Kept a separate map rather than a scope field on one, so that clearing
	// the turn's grants cannot reach the session's by accident — that
	// regression would be silent, and would be exactly the session-wide
	// silence on shell commands the ConfirmResult comment warns against.
	sessionAutoApprove map[string]bool
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
		OfferCode:            true,
		MaxSteps:             25,
		MaxErrorReflections:  3,
		LoopDetection:        true,
		toolLoops:            newToolLoopWatcher(),
		Tokens:               RuneCounter{},
		Clock:                RealClock{},
		Out:                  &StdOutput{},
		Prompts:              promptsForFormat(model.EditFormat),
		editFormat:           model.EditFormat,
		Files:                workspace.New(root),
		shown:                newShownFiles(),
		turnEditedFiles:      map[string]bool{},
		turnAutoApprove:      map[string]bool{},
		sessionAutoApprove:   map[string]bool{},
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
func (c *Coder) isPinned(path string) bool {
	abs := c.absRootPath(path)
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
	return workspace.ResolveSymlinks(filepath.Clean(abs))
}

// DisplayPath is how the UI and the prompt name one file, from a path given
// either root-relative or absolute.
//
// Root-relative inside the project. Root-relative one level up too — a file
// beside the project directory, or one inside a sibling of it — because
// ../spec.md and ../sibling-repo/include/api.h say something an absolute path
// does not: that the file sits next to the work. Absolute beyond that.
//
// The relative-inside, absolute-outside half is not a new rule; toProjectPaths
// has written the resume file that way all along, on the grounds that a
// reference reached outside the project has no project-relative form worth
// keeping. The one-level exception is display's own, and storage deliberately
// does not take it: a stored ../sibling breaks the moment the project moves,
// while a name on screen only has to be right now. Two jobs, two rules, and the
// divergence is in a file nobody reads.
//
// What it replaces named an out-of-tree pin by counting back to it, so
// /usr/include/foo.h from a project three levels down read as
// ../../../usr/include/foo.h — correct, and reading as though the file were
// part of the project when the whole point of /read-only is that it is not.
//
// The prompt gets this form too (readOnlyFilesContent), so the user and the
// model call the file the same thing. That works because Workspace.contain
// admits an absolute path for a pinned file, the same exemption unsafePath
// makes — which it did not until this rule needed it.
func (c *Coder) DisplayPath(path string) string {
	return c.displayName(c.absRootPath(path))
}

// displayName is DisplayPath for a caller that already holds a resolved
// absolute path, skipping the re-resolve. Everything that names a pinned file
// goes through one of the two.
func (c *Coder) displayName(abs string) string {
	rel := c.relFname(abs)
	up, rest := 0, rel
	for {
		if rest == ".." {
			up++
			rest = ""
			break
		}
		if !strings.HasPrefix(rest, "../") {
			break
		}
		up++
		rest = rest[len("../"):]
	}
	// rest == "" is the project's parent directory itself, which is not a file
	// anyone pins; it falls through to the absolute form rather than being
	// named "..".
	if up == 0 || (up == 1 && rest != "") {
		return rel
	}
	// Native separators, not ToSlash. A root-relative name is the tool-facing
	// form — read, grep, and ls all report forward slashes, and it has to match
	// them. An absolute name is not in that world: it is a path outside the
	// project, which the user will read, copy, and paste somewhere else, so it
	// should look the way their OS spells one. CI caught the difference as
	// C:/Users/... where Windows means C:\Users\...
	return filepath.Clean(abs)
}

func (c *Coder) relFname(abs string) string {
	rel, err := filepath.Rel(workspace.ResolveSymlinks(c.Root), abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

func (c *Coder) inchatRelativeFiles() []string {
	set := map[string]bool{}
	for _, f := range c.absFnames {
		set[c.displayName(f)] = true
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
	c.recordToolLines()
	c.turnEditedFiles = map[string]bool{}
	c.turnAutoApprove = map[string]bool{}
	c.editsExact, c.editsFuzzy = 0, 0
	// sessionAutoApprove is not reset here. That is the whole of the session
	// scope; /reset and "/web reset" are what end it.
	c.numReflections = 0
	c.numSteps = 0
	c.autoChecks = 0
	// The tool-loop watcher follows LoopDetection, the same switch the text
	// detector answers to — a user who turned detection off asked for neither
	// kind of interruption.
	if c.LoopDetection {
		c.toolLoops = newToolLoopWatcher()
	} else {
		c.toolLoops = nil
	}
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

// confirmGrouped wraps c.Confirm with group-scoped auto-approve. If the user
// answered "a" to a previous Confirm with the same Group, this one is approved
// without prompting. The first "a" answer records the group and returns true.
// req.GroupSession picks how long the record lasts — the session, or the turn.
// Callers that don't need grouping — confirmMoreSteps, checkTokens — call
// c.Confirm.Confirm directly.
//
// Both maps are read on every call regardless of scope. A group is only ever
// written to one of them, so this cannot widen a grant; what it does is keep a
// session grant honored by a request that forgot to set GroupSession.
func (c *Coder) confirmGrouped(req ConfirmRequest) bool {
	if req.Group != "" && (c.turnAutoApprove[req.Group] || c.sessionAutoApprove[req.Group]) {
		return true
	}
	res := c.Confirm.Confirm(req)
	if res.Always && req.Group != "" {
		if req.GroupSession {
			c.sessionAutoApprove[req.Group] = true
		} else {
			c.turnAutoApprove[req.Group] = true
		}
	}
	return res.Yes || res.Always
}

// Run executes one scripted message (script mode) and returns the turn's
// answer: every interrupted send's content in order, each steer as a
// blockquote, and the final send's content — the same string the transcript
// records.
//
// A panic inside the turn is recovered once, handed to OnCrash, and re-raised:
// the transcript keeps the turn's partial answer (the caller appends its tool
// lines and edited files from the same accessors it uses after a normal turn —
// the panic unwound through runOne's defer, which settled those), and the
// process still dies with the original stack trace. A recovered-and-swallowed
// panic in a coding agent leaves the tree half-edited and the session running
// on a broken coder; dying is the honest outcome. What recovery buys is that
// the work the turn already did is not lost with the process.
func (c *Coder) Run(ctx context.Context, withMessage string) (answer string) {
	defer func() {
		if r := recover(); r != nil {
			if c.OnCrash != nil {
				c.OnCrash(c.turnHistory + c.multiResponseContent + c.partialResponseContent)
			}
			panic(r)
		}
	}()
	c.runOne(ctx, withMessage)
	return c.turnHistory + c.multiResponseContent + c.partialResponseContent
}

// runOne is the turn loop. It keeps sending while the model has something to
// react to — a tool result it hasn't seen (OutcomeContinue) or a mistake to fix
// (OutcomeReflect) — and stops when the model has nothing left to say, the
// human interrupts, or a budget is spent. The turn boundary is still the
// human's: nothing here starts new work of its own.
func (c *Coder) runOne(ctx context.Context, userMessage string) {
	c.initBeforeMessage()
	c.turnHistory = ""

	if userMessage == "" {
		return
	}
	message := userMessage

	// Committing and rotating settled history are both turn-end concerns.
	// Mid-loop the tool results must stay in cur, and summarizing them away
	// between steps would compact the very results the next send reacts to;
	// committing mid-loop would spend one commit per send on fragments of one
	// change. A defer covers every exit — budget declined, reflection cap,
	// interrupted, or done. An interrupted turn's edits are real, so they are
	// committed too.
	defer func() {
		c.settleEdits("")
		c.endTurnHistory()
		// Last, so the accounting closes the turn: the commit line above it, and
		// nothing under it. This is what the per-send reorder was reaching for,
		// now at the scope where a reader actually wants the number. Once per
		// turn even when the turn was steered, because a steered turn is still
		// one turn's spend.
		c.flushTurnUsage()
	}()

	// Outcome-driven so a re-send that carries no message text — both a tool
	// reflection and a tool continuation re-enter on the appended tool results
	// — keeps the loop going.
	for {
		outcome, reflection := c.sendSteerable(ctx, message)
		// After the send, not during it: sendMessage does its interrupt
		// handling before returning, and that handling can retract messages.
		c.recordNewMessages()
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

		case OutcomeSelfInterrupted:
			// The model ended its own turn. Work so far is applied (the
			// normal turn-end machinery settles it); no question to the user,
			// who is simply told the turn stopped and why.
			return

		case OutcomeInterrupted:
			next, keepGoing := c.afterInterrupt()
			if !keepGoing {
				return
			}
			c.accumulateInterrupt(next)
			message = next

		case OutcomeLooping:
			next, keepGoing := c.afterLoop()
			if !keepGoing {
				return
			}
			c.accumulateInterrupt(next)
			message = next

		default:
			return
		}
	}
}

// accumulateInterrupt saves the content of a send that was stopped before the
// next send resets it, so the transcript can show the full arc of a steered
// turn: what the model was saying, the steer, and the final answer.
//
// The steer is included when the user typed a custom correction rather than
// choosing "Continue" (which carries no user text). A blockquote prefix makes
// the authorship unambiguous in the markdown transcript.
func (c *Coder) accumulateInterrupt(steer string) {
	if c.partialResponseContent != "" {
		c.turnHistory += c.partialResponseContent + "\n\n"
	}
	if steer != "" {
		c.turnHistory += "> " + strings.ReplaceAll(steer, "\n", "\n> ") + "\n\n"
	}
}

// sendSteerable runs one send under a context of its own, so that stopping the
// send does not have to mean ending the turn.
//
// This is the whole of the change that makes steering possible. Ctrl-C used to
// cancel the *turn's* context: everything downstream saw Canceled from then on,
// and there was no way back into the loop even though the conversation itself
// had survived intact. A child context per send moves the blast radius to the
// one send, and the turn context stays live to make the next one.
//
// The tool calls of a send run inside it too — applyToolCalls takes this same
// context — so Ctrl-C still kills a bash command that is running, which is the
// property worth not losing.
func (c *Coder) sendSteerable(ctx context.Context, message string) (SendOutcome, string) {
	sctx, cancel := context.WithCancel(ctx)
	c.sendMu.Lock()
	c.cancelSend = cancel
	c.sendMu.Unlock()

	defer func() {
		c.sendMu.Lock()
		c.cancelSend = nil
		c.sendMu.Unlock()
		cancel()
	}()

	return c.sendMessage(sctx, message)
}

// InterruptSend stops the send in flight without ending the turn.
//
// Called from the REPL's signal handler, which used to cancel the turn context
// directly. A Ctrl-C between sends finds no send to cancel and does nothing,
// which is correct: there is nothing in flight, and the prompt the user is
// about to get asked is the interrupt they wanted.
func (c *Coder) InterruptSend() {
	c.sendMu.Lock()
	cancel := c.cancelSend
	c.sendMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// settleEdits commits the turn's edits so far and records a snapshot to undo
// them by.
//
// Factored out of the turn-end defer because an interruption is also a place
// this belongs. The human stopped to look at something, which makes that point
// a review boundary: `git show` gives exactly what the model did before it was
// stopped, separately from what it did after the correction, and /undo steps
// through the two halves one at a time. Anyone who wanted one commit has
// /squash.
//
// What is *not* here is flushTurnUsage. The spend is a property of the turn,
// not of each leg of it, so it stays in the defer and reports once.
func (c *Coder) settleEdits(message string) {
	// Nothing written since the last settle, so there is nothing to settle.
	// Without this an interrupted-then-steered turn settles twice over the same
	// edits: the second commit finds the tree already matching HEAD and
	// announces "the turn left the files as they were", which is true of the
	// second attempt and false of the turn.
	//
	// turnSnap is the right signal rather than turnEditedFiles, which
	// accumulates across the whole turn because the history record at turn end
	// wants every file the turn touched. turnSnap is emptied by
	// pushTurnSnapshot and rebuilt by recordWrites, so it means precisely
	// "written since the last settle".
	if c.turnSnap.empty() {
		return
	}
	c.commitTurn(message)
	c.pushTurnSnapshot()
}

// afterInterrupt asks the human what a stopped turn should do next, and
// reports the message to resume with and whether to resume at all.
//
// The choice is put through the Asker port, which is what ask_user_question
// already uses to prompt in the middle of a turn — so this is a shape the
// terminal handling has been driving all along rather than a new one. Free text
// comes back as the raw line, which *is* the correction, so there is nothing to
// parse. A nil Asker means no interactive terminal (script mode): stopping is
// the only honest answer there, and it is what an interrupt has always done.
//
// The --yes flags deliberately do not answer this. They skip permission
// prompts, and being asked what you meant by stopping is not a permission
// prompt — the same line the port's own documentation draws for
// ask_user_question.
func (c *Coder) afterInterrupt() (message string, keepGoing bool) {
	if c.Asker == nil {
		return "", false
	}
	// The edits so far belong to the interrupt, whichever way this goes.
	c.settleEdits("")

	answer := c.Asker.Ask(AskRequest{
		Question: "You stopped the model. What now?",
		Options: []AskOption{
			{Label: "Continue", Description: "Carry on from where it was cut off"},
			{Label: "Stop", Description: "End the turn here"},
		},
	})
	if len(answer) == 0 {
		return "", false
	}
	switch answer[0] {
	case "Continue":
		// Say what the user decided, or the model is answering the wrong
		// question.
		//
		// noteInterrupt runs before this prompt, so all it can say is that the
		// reply was cut off. On its own that reads as a full stop, and the
		// model obliges: "No problem — I'll stop here. What would you like me
		// to do next?" It is not misreading anything. Asked afterwards what it
		// had been told, it quoted the cut-off note and said no continue choice
		// had reached it, which was exactly right.
		//
		// The rest of the wording closes the other failure mode. With a whole
		// partial reply above it and no instruction about what to do with it, a
		// model will also quite reasonably begin again from the top — which is
		// what an earlier live pass showed, and what was wrongly written up as
		// the cost of keeping the partial rather than as this bug.
		c.curMessages = append(c.curMessages, llm.HarnessNote(
			"The user chose to continue. Pick up exactly where your reply above "+
				"stops and carry on from there — do not start over, do not repeat "+
				"or summarize what you already wrote, and do not ask what to do next."))
		// The notes are the message, so the next send re-enters on them rather
		// than appending an empty user turn to carry nothing.
		c.resumeInPlace = true
		return "", true
	case "Stop":
		return "", false
	default:
		// Anything else the user typed is the steer, and it goes in as their
		// own words in their own voice. They really did type it.
		return answer[0], true
	}
}

// afterLoop asks what to do about a reply the harness stopped for repeating
// itself, and reports whether the turn should continue.
//
// Its own function rather than a branch of afterInterrupt, because both halves
// differ. The question cannot say "you stopped the model" — the user did not —
// and "Continue" cannot mean "pick up exactly where your reply stops", which
// here is an instruction to resume the loop. What is offered instead is a
// retry: the loop's note (send.go) tells the model to take a different
// approach, and the most useful thing the user can do is type what that should
// be, which the free-text steer already carries.
func (c *Coder) afterLoop() (message string, keepGoing bool) {
	if c.Asker == nil {
		return "", false
	}
	// The edits so far belong to the stopped reply, whichever way this goes.
	c.settleEdits("")

	answer := c.Asker.Ask(AskRequest{
		Question: "The model was repeating itself and was stopped. What now?",
		Options: []AskOption{
			{Label: "Stop", Description: "End the turn here"},
			{Label: "Try again", Description: "Let it answer again, told not to repeat itself"},
		},
	})
	if len(answer) == 0 {
		return "", false
	}
	switch answer[0] {
	case "Stop":
		return "", false
	case "Try again":
		// The note noteLoop already appended is the whole message: it says what
		// was stopped and what to do instead, so the next send re-enters on it
		// rather than appending an empty user turn to carry nothing.
		c.resumeInPlace = true
		return "", true
	default:
		// Anything else the user typed is the steer, in their own voice.
		return answer[0], true
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

	res := c.Confirm.Confirm(ConfirmRequest{Prompt: "Keep going?", Grant: GrantSteps})
	if !res.Yes {
		c.Out.Printf("Stopping here. The work so far is applied; say what to do next.")
		return false
	}
	c.numSteps = 0
	c.autoChecks = 0
	c.editedSinceCheck = false
	if c.LoopDetection {
		c.toolLoops = newToolLoopWatcher()
	}
	return true
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

// ToolBlock writes the shaped program block to the screen. It carries no
// record: the tee deliberately ignores it and captures the prose summary
// instead — a flattened copy of the block would be the unreadable line this
// rendering exists to replace.
func (o *StdOutput) ToolBlock(title, body string) {
	render.ToolBlock(os.Stdout, title, body)
	o.sep.Drew()
}

// Link outside the REPL is a plain sanitized line: script mode's output is
// captured as often as it is read, and an OSC 8 escape there is the hazard
// doc/experimenting.md records breaking a scorer.
func (o *StdOutput) Link(target string) {
	fmt.Print(render.Sanitize(target) + "\n")
	o.sep.Clear()
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

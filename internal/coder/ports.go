// Package coder is the orchestration spine: assemble -> stream -> reflect ->
// apply -> shell -> commit -> cost.
package coder

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// TokenCounter estimates token counts. Consumers treat counts as
// advisory-conservative and never gate irreversibly on an estimate.
type TokenCounter interface {
	Count(text string) int
}

// RuneCounter is the default estimator: code points / 4. Measured
// against the phase-0 captures, runes/4 lands within ~0.5% of the
// provider's real prompt_tokens for the primary model (DeepSeek: 3.99–4.02
// chars/token; est/real ≈ 1.00 over five requests).
// It can under-count unusually code-dense payloads (code runs closer to
// 3.3 chars/token), so the count is advisory, not a guarantee; consumers
// never gate irreversibly on it (margin: unknown-max→always-add).
type RuneCounter struct{}

func (RuneCounter) Count(text string) int {
	n := 0
	for range text {
		n++
	}
	return n / 4
}

// Confirmer asks the user yes/no questions.
type Confirmer interface {
	Confirm(req ConfirmRequest) ConfirmResult
}

// ConfirmResult is the user's answer to a confirmation prompt.
//
// There used to be a Never ("d", don't ask again) alongside these, offered by
// the prompt whenever AllowNever was set — and honored by nothing. Each caller
// treated it as a plain decline: the URL check rejects the URL either way, the
// command-output check reads only Yes, and the shell gate goes through
// confirmGrouped, which never looked at it. The one caller that might have meant
// it left with the file-mention flow. A prompt advertising an option that does
// nothing is worse than one without it, so the option is gone rather than
// implemented — session-scoped silence on shell commands is the last thing this
// gate should grow.
type ConfirmResult struct {
	Yes bool // the user approved this one action
	// Always is "a": auto-approve this Group for as long as its scope lasts.
	// The answer does not carry the scope — the request does, in GroupSession —
	// because the user is answering the question they were shown, and it is the
	// asker that decided how long that answer is good for.
	Always bool
}

// The permission names --yes takes. Two kinds, deliberately in one flag: the
// first three grant the model a capability, the last two answer a question the
// harness asks about its own pacing. Both need a name for a session with no
// terminal to answer on, and a name that says which prompt it covers beats a
// flag that means "everything except the scary one".
const (
	GrantBash      = "bash"      // run a shell command the model wrote
	GrantWebfetch  = "webfetch"  // fetch a URL the model chose
	GrantWebsearch = "websearch" // send the model's query to the configured backend
	// GrantSteps answers "Keep going?" at the step budget. Not a capability:
	// the model gains nothing it did not have, the turn simply continues. Worth
	// knowing before typing it that the budget resets each time it is answered,
	// so granting this is granting an unbounded number of steps — max_steps
	// stops being a limit and becomes an interval.
	GrantSteps = "steps"
	// GrantContext answers "Try to proceed anyway?" when the estimated request
	// exceeds the model's input limit. Also not a capability: the request is
	// sent and the provider decides, which is what the prompt's own text says
	// is probably fine.
	GrantContext = "context"
	// GrantAll is every name above. A word someone types, never a default.
	GrantAll = "all"
)

// GrantNames are the individual permissions, in the order help text lists them.
var GrantNames = []string{GrantBash, GrantWebfetch, GrantWebsearch, GrantSteps, GrantContext}

// ParseGrants turns --yes values into the set AutoConfirmer reads. Each value
// may be a comma-separated list, and the flag may repeat, so
// "--yes bash --yes webfetch,websearch" and "--yes bash,webfetch,websearch"
// are the same thing. An unknown name is an error naming what would have
// worked, rather than a silent no-op that looks like a permission was granted.
func ParseGrants(values []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, v := range values {
		for name := range strings.SplitSeq(v, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if name == GrantAll {
				for _, g := range GrantNames {
					out[g] = true
				}
				continue
			}
			if !slices.Contains(GrantNames, name) {
				return nil, fmt.Errorf("--yes %s: unknown name (want %s, or %q for all of them)",
					name, strings.Join(GrantNames, ", "), GrantAll)
			}
			out[name] = true
		}
	}
	return out, nil
}

// ConfirmRequest mirrors aider's confirm_ask surface.
//
// Command is a shell command awaiting approval, and Purpose is the model's own
// claim about what running it is for. They are separate from Subject rather
// than folded into it because a renderer should be free to draw a command
// differently from a URL — and because a request carrying a Command is one
// whose asker was *asked* for a purpose, which is what makes an empty Purpose
// worth showing as an absence rather than passing over in silence. The coder
// stays ignorant of how any of the three is drawn.
type ConfirmRequest struct {
	Prompt  string
	Command string
	// URL and Origin are webfetch's pair: the whole URL, which is what has to
	// be read, and the host:port it reduces to, which is what an "all this
	// origin" answer covers. Both are shown, because a prompt that scopes an
	// answer to something it never printed is asking blind.
	URL    string
	Origin string
	// Query is websearch's: the text the model is about to send to the user's
	// search instance. Separate from Command and URL because it is neither —
	// there is no destination to weigh here, the user already pinned that in
	// their config, so the query is the whole of what there is to read.
	Query   string
	Purpose string
	// Grant names which permission this prompt asks for, and so which
	// --yes NAME answers it. One of the Grant* constants; empty means no flag
	// can answer it and the terminal always decides.
	//
	// It replaced a RequiresYesShell bool, which was one tool's name encoded as
	// a boolean: it could say "this is the shell one" and nothing else, so a
	// third gated tool had no way to be named without a third flag.
	Grant string
	Group string // ConfirmGroup key ("all"/"skip" scope)
	// GroupSession makes an "a" answer last for the session rather than the
	// turn. Only webfetch sets it, and the asymmetry with the shell gate is the
	// point: an "a" on shell is licensed by the sandbox, which bounds what an
	// unseen command can do, so it can afford to be broad in *what* and must
	// stay narrow in *when*. Nothing bounds an unseen URL, so the shell gate
	// keeps the turn boundary and webfetch buys its longer life by scoping to
	// one origin and saying so at the prompt. Turn scope never paid for
	// webfetch: a turn holds one or two fetches, so "a" saved a single prompt
	// and asked again about the same host on the next turn.
	GroupSession bool
}

// AskRequest is one question put to the user on the attached terminal. The
// options are the modeled choices; the rendered list always adds a final
// "Other — type your own answer" row, which is why an answer can come back
// as raw text rather than a label.
type AskRequest struct {
	Question    string
	Options     []AskOption
	MultiSelect bool
}

// Asker puts one question to the user and returns their answer: the chosen
// labels (one entry, or several for multiSelect), or the raw text they typed.
// A nil Asker means no interactive terminal is attached (script mode); the
// caller answers the model with an error result rather than asking.
//
// A port of its own rather than a widening of Confirmer, whose yes/no/always
// shape cannot carry a multiple-choice question. The split is also the reason
// --yes cannot answer one: that flag skips named permission prompts,
// and a question is not a permission prompt — it is the model asking for
// information it cannot proceed without.
type Asker interface {
	Ask(req AskRequest) []string
}

// ParseAskAnswer maps the line the user typed at a question prompt to the
// answer the model receives: the labels the indices select, or the raw input
// as one free-text answer. It is exported because two implementations of
// Asker need the identical rule — the REPL's terminal prompt and the fixture
// harness's scripted rows — and an answer interpreted differently between the
// two would make a replay lie about what a live session did.
//
// The rule, mirroring Claude Code's reference implementation: split on
// commas, and only if *every* token is a valid index in range do the indices
// select labels. Anything else — an out-of-range number, a multi-index
// answer to a single-select question, prose — is the whole raw input, never
// a partial interpretation, because silently dropping the tokens that didn't
// parse produces answers the user didn't intend.
//
// The index of the "Other" row (len(Options)+1) is the one special case: typed
// alone it is a custom answer the user left blank, so the answer is empty
// rather than the meaningless raw string "3".
func ParseAskAnswer(req AskRequest, input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	if n, err := strconv.Atoi(input); err == nil && n == len(req.Options)+1 {
		return nil
	}
	tokens := strings.Split(input, ",")
	labels := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		i, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil || i < 1 || i > len(req.Options) {
			return []string{input}
		}
		labels = append(labels, req.Options[i-1].Label)
	}
	if !req.MultiSelect && len(labels) != 1 {
		return []string{input}
	}
	return labels
}

// AutoConfirmer implements --yes NAME: it answers a prompt whose Grant the
// user named, and defers everything else. There is no blanket form, which is
// the whole change — the flag pair it replaced meant "yes to everything except
// the shell", a shape that could not name a third thing worth withholding.
type AutoConfirmer struct {
	// Granted holds the names from --yes. A prompt is answered only when its
	// own Grant is in here — there is no blanket yes, because a blanket yes is
	// what "--yes, except the dangerous one" was, and that shape cannot say
	// which one without naming it in the flag itself.
	Granted map[string]bool
	// Fallback handles prompts the flags don't answer; nil declines.
	Fallback Confirmer
}

func (a AutoConfirmer) Confirm(req ConfirmRequest) ConfirmResult {
	// An unnamed prompt is never answered by a flag. That is deliberate rather
	// than an oversight: a prompt with no Grant has no name a user could have
	// typed, so answering it would be answering something they never asked for.
	if req.Grant != "" && a.Granted[req.Grant] {
		return ConfirmResult{Yes: true}
	}
	if a.Fallback != nil {
		return a.Fallback.Confirm(req)
	}
	return ConfirmResult{}
}

// CommandRunner executes one accepted shell block through a single shell.
// Output merges stdout and stderr.
type CommandRunner interface {
	Run(ctx context.Context, block string, cwd string) (exitCode int, output string, err error)
}

// Repo is the git port; nil Repo means no git integration this session.
// Implemented by shelling out in phase 8.
type Repo interface {
	Root() string
	TrackedFiles() []string // repo-root-relative
	PathInRepo(rel string) bool
	IsDirty(rel string) bool
	GitIgnored(rel string) bool
	HeadSHA() string
	// Commit commits fnames; returns hash and message, or ok=false when there
	// was nothing to commit. attributed marks auto-commits of model edits,
	// which get the trailer; dirty commits of user changes stay unattributed.
	//
	// An empty message is generated from the staged diff and context, which is
	// the automatic path and the only one there used to be. A non-empty one is
	// used verbatim: the commit tool lets the model write its own, and the
	// model that made the change knows why it made it, where the generator is
	// a side model inferring intent from a diff.
	Commit(fnames []string, context, message string, attributed bool) (hash, message2 string, ok bool, err error)
}

// Clock injects time so retry/continuation tests don't sleep.
type Clock interface {
	Sleep(d time.Duration)
	Now() time.Time
}

// RealClock is the production clock.
type RealClock struct{}

func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }
func (RealClock) Now() time.Time        { return time.Now() }

// Output is where the coder talks to the user. StreamText receives answer
// deltas as they arrive; StreamReasoning receives reasoning deltas
// (display-only, never parsed or persisted).
type Output interface {
	Printf(format string, args ...any)
	Warningf(format string, args ...any)
	Errorf(format string, args ...any)
	// Toolf reports what a tool just did — the file that was read, the search
	// that matched, the check that passed. Most of a turn's scroll is now this,
	// so it gets a recessive color of its own: the harness narrating its own
	// work should sit behind the diffs and the answer, not compete with them.
	Toolf(format string, args ...any)
	// Link prints one URL on its own line, hyperlinked where the terminal can
	// and plain where it cannot. Separate from Printf because a URL reaching
	// the screen is model-supplied text next to escapes Strument writes: this
	// is the one renderer that sanitizes it and drops the hyperlink for
	// anything suspect. Every URL the user is shown goes through it, so the
	// page named at a prompt and the page named by a fetch nobody was asked
	// about are drawn the same way.
	Link(target string)
	StreamText(delta string)
	StreamReasoning(delta string)
	// StreamToolCall receives a streamed tool-call argument fragment for the
	// call at index (name on the first fragment for an index), so edit tools
	// render as a live red-green diff. FlushStream closes any open render.
	StreamToolCall(index int, name, argsFragment string)
	// FlushStream marks the end of a send's live rendering.
	FlushStream()
}

// ScrapeOptions are the ways a caller can ask for less than a whole page.
//
// The fragment is not here: it rides in the URL, where the model writes it and
// where every link on the page already carries one. Only the outline needs
// saying separately.
type ScrapeOptions struct {
	// Outline returns the page's headings and their anchors instead of its
	// content — the map you read before deciding which section to fetch.
	Outline bool
}

// Scraper fetches URL content; injectable for tests.
type Scraper func(ctx context.Context, url string, opts ScrapeOptions) (string, error)

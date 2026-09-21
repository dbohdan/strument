// Package config implements Strument's Starlark configuration surface and
// the direnv-style trust gate for project configs.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// Adapters recognized by provider(). Each names a wire dialect plus the
// defaults of one destination.
//
// openai, openrouter and opencode speak OpenAI chat-completions; anthropic and
// opencode-anthropic speak Anthropic Messages; responses and
// opencode-responses speak OpenAI's Responses API. opencode Go enforces which
// of its models is on which — see doc/config.md.
const (
	AdapterOpenAI     = "openai"
	AdapterOpenRouter = "openrouter"
	AdapterOpenCode   = "opencode"

	// AdapterAnthropic and AdapterOpenCodeAnthropic speak Anthropic Messages.
	// Dialect and destination are separate choices — "anthropic" with a
	// base_url reaches any gateway that speaks it — but opencode serves three
	// dialects from one host and one key, so it gets a name per dialect rather
	// than the destination being inferred from a URL.
	AdapterAnthropic         = "anthropic"
	AdapterOpenCodeAnthropic = "opencode-anthropic"

	// AdapterResponses and AdapterOpenCodeResponses speak OpenAI's Responses
	// API, the third of the protocols opencode Go serves.
	AdapterResponses         = "responses"
	AdapterOpenCodeResponses = "opencode-responses"
)

// Edit formats recognized by model(). Only one remains, and the parameter is
// kept for the configs that name it: "diff", "diff-fenced", and "whole" were
// for models that could not call functions reliably, and such a model cannot
// drive this harness at all now — finding and reading files are tool calls too.
//
// "ask" is deliberately absent: it is a runtime-only mode (the /ask command),
// not a configurable one — a model whose default mode was "ask" could never
// edit anything.
var knownEditFormats = map[string]bool{
	"tool": true,
}

// retiredEditFormats get a message that says what happened, rather than a bare
// "unknown": a config carrying one of these worked until this change.
var retiredEditFormats = map[string]bool{
	"diff":        true,
	"diff-fenced": true,
	"whole":       true,
}

// reservedParamKeys are transport keys Strument owns; extra_params cannot
// override them. The value, when non-empty, is the setting that does own the
// key, so the refusal can say what to write instead of only what not to.
//
// The output cap is fenced because every dialect writes it after the
// passthrough — max_tokens here and on Anthropic, max_output_tokens on
// Responses — so a passthrough entry would be silently overridden rather than
// honored. It was reachable before only because the OpenAI dialect was not
// writing the field at all, which was the bug, not the feature.
var reservedParamKeys = map[string]string{
	"model":             "",
	"messages":          "",
	"stream":            "",
	"stream_options":    "",
	"usage":             "",
	"max_tokens":        "max_output",
	"max_output_tokens": "max_output",
}

// Provider is a pure carrier of endpoint + dialect; no behavior inheritance.
type Provider struct {
	// One of the Adapter* constants above.
	Adapter     string
	BaseURL     string // "" => adapter default
	APIKey      string
	Name        string
	Proxy       string         // resolved SOCKS5 proxy URL; "" => direct (no proxy)
	ExtraParams map[string]any // JSON-only, reserved keys rejected
}

// GroupKey groups models onto one runtime client/connection pool per
// endpoint (value semantics; grouping by adapter+base_url+proxy).
func (p Provider) GroupKey() string {
	return p.Adapter + "\x00" + p.BaseURL + "\x00" + p.Proxy
}

// Model is one usable model declaration.
type Model struct {
	Provider     Provider
	Slug         string
	DisplayName  string // human-readable label; "" => derived from Slug
	EditFormat   string // "tool" | "diff" | "diff-fenced" | "whole"
	SideModel    *Model // non-nil after resolution (self if unset)
	Reasoning    string // request-side effort: "low"/"medium"/"high"; "off" disables; "" or "default" => provider default
	ReasoningTag string // response-side inline tag to strip; "" => none
	Temperature  *float64
	RepoMap      bool
	Cache        bool // enable prompt-cache breakpoints (1h TTL)
	Context      int  // input window tokens; 0 => unknown
	MaxOutput    int
	// Prefill says this model continues a partial assistant message instead of
	// answering afresh. It is what lets a reply stopped by max_output be
	// resumed: send.go appends what came back as an assistant turn and asks
	// again, stitching the pieces.
	//
	// Default false, which is the conservative half of an asymmetry. Wrong-false
	// costs a truncated answer and a warning saying so, and the user can ask for
	// the rest. Wrong-true is silent and expensive: a model that does not
	// continue reads the appended text as something the *user* wrote and answers
	// about it, so the stitch is the partial answer glued to a fresh one. Live,
	// against a 120-token cap, MiMo-V2.5 narrated "the user has provided what
	// appears to be a partial response to their own request", burned all four
	// continuations (600 tokens every run) and produced nothing usable, where
	// Claude Haiku 4.5 finished cleanly in 444.
	//
	// It is per model, not per provider: over one OpenRouter endpoint,
	// claude-haiku-4.5, gemini-3.1-flash-lite, mistral-small-3.2,
	// llama-3.3-70b and deepseek-v4-flash-0731 continued 3/3, while
	// mimo-v2.5, glm-5.3-flash, deepseek-v4.1-flash, gpt-4.1-nano and
	// gpt-5-nano restarted 3/3. Nothing about the adapter predicts which.
	Prefill bool
	// InputModalities names the content kinds this model accepts besides text,
	// using the same strings as the llm.Block* kinds so a projection can look a
	// block up directly. Empty means text only, which is the safe default: a
	// model that can see images but is not declared to gets a text label, while
	// the reverse would be a request the provider rejects.
	InputModalities []string
	InputCost       *llm.Money // per-token USD (config declares per-million); nil => unknown (never fabricate cost)
	OutputCost      *llm.Money
	ExtraParams     map[string]any

	// sideRef holds an unresolved string alias or inline model between
	// construction and resolution.
	sideRef any
}

// Accepts reports whether this model takes content blocks of the given kind.
//
// Text is unconditional. There is no model that does not take text, and making
// it depend on the declaration would let a config with
// input_modalities = ["image"] produce a model nothing can be said to.
func (m *Model) Accepts(kind string) bool {
	if kind == llm.BlockText {
		return true
	}
	return slices.Contains(m.InputModalities, kind)
}

// SlugCore reduces a model slug to its core name: everything after the last
// "/" (dropping the provider prefix) and before the first ":" (dropping a
// ":variant" suffix, which can name a private endpoint). Falls back to the full
// slug when that reduction is empty. It is the default display name, and
// `strument model-config` reuses it as the dict-key alias.
func SlugCore(slug string) string {
	s := slug
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return slug
	}
	return s
}

// ReadableName is the human-facing model name used in commit trailers: the
// configured display_name, or the slug reduced to its core (see SlugCore).
func (m *Model) ReadableName() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return SlugCore(m.Slug)
}

// QualifiedSlug is the provider-qualified model slug: the provider's name (its
// adapter when unnamed) joined to the slug, e.g. "openrouter/xiaomi/mimo-v2.5"
// or "local/qwen/qwen3.6-27b". Shown wherever the user sees a slug, it makes an
// endpoint diagnosable at a glance — which provider is this model on? — and
// converges on aider's provider-prefixed model names.
func (m *Model) QualifiedSlug() string {
	prov := m.Provider.Name
	if prov == "" {
		prov = m.Provider.Adapter
	}
	return prov + "/" + m.Slug
}

// RequestExtraParams merges provider-scoped and model-scoped extra_params,
// model over provider.
func (m *Model) RequestExtraParams() map[string]any {
	if len(m.Provider.ExtraParams) == 0 && len(m.ExtraParams) == 0 {
		return nil
	}
	out := make(map[string]any, len(m.Provider.ExtraParams)+len(m.ExtraParams))
	maps.Copy(out, m.Provider.ExtraParams)
	maps.Copy(out, m.ExtraParams)
	return out
}

// SandboxLandlock is the only confinement mechanism Strument implements. It is
// a named string rather than a boolean because "sandboxed" is not one thing:
// a future macOS or Windows backend would be a different mechanism with
// different guarantees, and a config that says which one it got can be read
// years later and still mean something.
const SandboxLandlock = "landlock"

// Config is the host-facing result of the load pipeline.
type Config struct {
	Models  map[string]*Model // alias -> model
	Default string            // must be a key of Models
	// Proxy is the global fallback SOCKS5 proxy URL: it applies to
	// model-config, URL scraping, and any provider that sets no proxy of its
	// own ("" => no global proxy).
	Proxy string
	// Scraper, when non-empty, is an external command (argv, with %s marking the
	// URL) run to fetch pages instead of the built-in HTTP scraper — the opt-in
	// path for JavaScript-rendered pages. The global proxy does not apply to it.
	Scraper []string
	// Check is the project's named verification commands, in declared order.
	// The `check` tool runs them by name; a run with no name runs all of them
	// in order and stops at the first failure, so fast checks belong first.
	Check []Check
	// CheckAuto names the checks the harness runs on its own at the end of a
	// turn that edited files, in the order given. Empty means the model is the
	// only thing that ever runs a check.
	CheckAuto []string
	// ReasoningDisplay is how much of the model's thinking to show. The zero
	// value shows all of it.
	ReasoningDisplay ReasoningDisplay
	// MaxSteps overrides the work-step budget per turn. 0 uses the built-in
	// default (25). The budget is a checkpoint, not a wall: on exhaustion the
	// user is shown what the turn has done and asked whether to keep going.
	MaxSteps int
	// UndoTurns is how many turns /undo can reach back through. 0 uses the
	// built-in default (20). It governs both the live stack and the saved one,
	// so the distance is the same in a session and after a restart.
	//
	// Raising it costs memory: a turn holds every touched file's contents
	// twice, before and after. The saved copy is separately bounded by bytes,
	// so a large value is paid for in the session rather than on disk.
	MaxUndoTurns int
	// MaxErrorReflections overrides the error-reflection budget per turn. 0
	// uses the built-in default (3). An error reflection is the model
	// recovering from its own mistake — a failed edit match, a bad shell
	// command — and should stay rare.
	MaxErrorReflections int
	// WebfetchAllow are origins — host, or host:port — the webfetch tool may
	// fetch without asking. Empty means every fetch is confirmed.
	//
	// It says which fetches skip the prompt, not which are reachable. The
	// difference is deliberate: `bash` can curl anywhere and Landlock does not
	// touch the network, so a restriction here would be a boundary the tool
	// beside it steps over. Matching is exact, and an entry without a port
	// covers only 80 and 443.
	WebfetchAllow []string

	// WebSearch configures the websearch tool. Nil means no search backend is
	// configured, and the tool is not offered at all — unlike webfetch, which
	// always has the built-in fetcher behind it, there is nothing to fall back
	// to here.
	WebSearch *WebSearch

	// NoLoopDetection turns off stopping a reply that has degenerated into
	// repeating itself. Named for what it overrides, not for what it does, so
	// that the zero value means the built-in default (on) — the same shape as
	// MaxSteps and the rest. `loop_detection = False` is the only thing that
	// sets it.
	NoLoopDetection bool

	// NoShell withholds the bash tool from the schema, rather than offering it
	// and refusing the calls. A prompt whose answer never changes teaches that
	// prompts are noise, and a tool the model may not use is worse than absent:
	// it plans around a capability it does not have, then spends a step finding
	// out. `shell = False` and --no-shell both set it. Execution stays gated
	// too, so a path that does not go through the tool cannot slip past.
	NoShell bool
	// AnchoredEdits gives read a stable identity per line and lets edit
	// address lines by it instead of by quoting them back.
	//
	// It exists because ambiguity is most of what makes an edit fail: 13 of 16
	// first-try failures across the model panel were "the text appears N
	// times", and an anchor names one line, so that cannot happen
	// (doc/experiments/2026-09-anchored-edit/m1.md). It costs about 4% more
	// input on every read, which is what the anchor column adds over the line
	// numbers read already prints.
	//
	// Off by default: the comparison that would justify a default is phase 1 of
	// that trial, and it has not run.
	AnchoredEdits bool
	// IndentColumn puts a line's leading whitespace in its own column, named in
	// words, so the model states indentation rather than reproducing it.
	//
	// It needs AnchoredEdits: it is the safety net anchoring removes. Under a
	// quoted-span edit the line matcher repairs whitespace drift; anchored
	// editing has no matching, and phase 1 measured 30 of 72 outputs coming
	// back misindented as a result.
	IndentColumn bool

	// ObservationViaRunCode turns on the code-observation force arm: the direct
	// read-only tools are withheld from the schema and all file observation
	// goes through code programs. Default false; `observation_via_run_code = True`
	// sets it. It is the experimental arm of the code-uptake trials
	// (doc/experiments/2026-09-code-mode2/README.md), not a supported mode — yet.
	ObservationViaRunCode bool

	// ExampleMessages are few-shot example messages appended to the prompt
	// set's example block, from the `example_messages` setting — a list of
	// [role, content] pairs. Empty means none. It exists for the
	// shell-parallelism trial's EX arm (doc/experiments/2026-09-shell-parallel/README.md):
	// the planning-side lever the code-only report named, which prose in the
	// system prompt could not supply on its own. Like observation_via_run_code it
	// is an experimental arm, not a supported mode — yet.
	ExampleMessages []ExampleMessage

	// Sandbox names the confinement mechanism: SandboxLandlock or "" for
	// none. It defaults to Landlock on Linux and "" elsewhere, and when it is
	// set it is a requirement rather than a preference — see doc/security.md.
	Sandbox string

	// SandboxWrite are extra absolute paths the sandbox permits writes under,
	// on top of the project, the state directory, a temporary directory and
	// the toolchain caches.
	SandboxWrite []string

	// ShellTimeout bounds one model-caused command, in seconds. 0 is unset
	// (the coder's two-minute default); -1 is the config's `shell_timeout = 0`,
	// meaning no limit. /run is never bounded — the user typed it.
	ShellTimeout int
	// GitSign is the commit-signing flag passed to `git commit`: "-S" to sign
	// with the default key, "-S<keyid>" to pick one, "" for unsigned. It comes
	// from the `git_sign` setting (a boolean or a key-id string).
	GitSign string
	// EnvAllow names environment variables to pass to model-run commands
	// (the bash tool, checks, the scraper command) on top of the built-in
	// default allowlist. See coder/envallow.go. Empty means defaults only.
	// Matching is exact; prefixes are not expanded.
	EnvAllow []string

	// AutoApprove names the confirmation prompts answered without asking, the
	// standing form of --yes. Validated at load against GrantNames.
	AutoApprove []string
	// EnvSet overrides environment variables for the whole session, from the
	// `env_set` setting. Applied to Strument's own process at startup, so every
	// subprocess inherits it: git, /run, and — for names env_allow also passes —
	// the model's commands. See ApplyEnvSet.
	//
	// It does not widen what the model sees. A name set here still has to be on
	// the allowlist to reach a model-run command, which is what keeps a value
	// written in a config file from being handed to the model by accident.
	EnvSet map[string]string

	// Prompt as a whole-string replacement or literal injector. Each is a
	// string setting that lands on the corresponding prompt, layered over the
	// built-in on top of whatever format's set is active. Empty ("", the
	// default) means the built-in wins. The prompt mechanism is two-tier:
	//
	//   - prompt_system_prefix is a Tier 0 literal injector: prepended verbatim
	//     to the active system prompt, with no placeholder substitution. It is
	//     how a user adds a standing directive without rewriting anything.
	//   - prompt_code / prompt_ask are Tier 1 whole-string replacements of the
	//     active mode's MainSystem. They share the built-ins' closed
	//     placeholder set ({platform}, {language}, {final_reminders},
	//     {code_tools}, {observation_tools}) and are validated at load.
	//   - prompt_commit / prompt_read_only are the same Tier 1 replacement for
	//     the commit-message system prompt and the read-only reference prefix.
	//
	// The prompt_ keys are project- and user-settable; the trust gate that
	// guards .strument.star is what makes a project's prompt overrides the
	// user's own decision. chat_language fills the {language}/{final_reminders}
	// slots (what detectUserLanguage reads as its explicit branch) instead of
	// relying on LANG-style environment variables.
	PromptSystemPrefix string
	PromptCode         string
	PromptAsk          string
	PromptCommit       string
	PromptReadOnly     string
	ChatLanguage       string
}

// ExampleMessage is one few-shot example: a role ("user" or "assistant") and
// its content.
type ExampleMessage struct {
	Role    string
	Content string
}

// ReasoningMode is what ReasoningDisplay does with a thinking block.
type ReasoningMode int

const (
	// ReasoningFull shows the whole block. The default, because a plain text
	// stream has no way to unfold what it hid, so anything less makes the
	// transcript incomplete — which is a thing to choose, not to inherit.
	ReasoningFull ReasoningMode = iota
	// ReasoningCapped shows the first Lines lines and says how many it left.
	ReasoningCapped
	// ReasoningOff shows nothing, not even a marker.
	ReasoningOff
)

// ReasoningDisplay is the `reasoning_display` setting: "full", a positive
// integer, or "off".
//
// It is about a screen, not about a request. "off" hides the thinking; it does
// not stop the model producing it, and reasoning tokens are billed either way.
// The per-model reasoning="off" is what stops the spending. Keeping these apart
// matters because a project's .strument.star could otherwise change what a turn
// costs by way of a display preference.
type ReasoningDisplay struct {
	Mode  ReasoningMode
	Lines int // meaningful only for ReasoningCapped
}

// Check is one named verification command: an argv, never a shell string.
//
// The name is what the model passes to the check tool, which is the point of
// naming them. The model never supplies a command, so there is nothing to
// classify and nothing to smuggle through — which is what lets checks run
// without the confirmation `bash` requires.
type Check struct {
	Name string
	Argv []string
}

// indexCheck returns the position of the named check, or -1.
func indexCheck(checks []Check, name string) int {
	for i, c := range checks {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// CheckNames lists the configured check names in declared order.
func (c *Config) CheckNames() []string {
	out := make([]string, 0, len(c.Check))
	for _, v := range c.Check {
		out = append(out, v.Name)
	}
	return out
}

// DefaultModel returns the model for the default alias.
func (c *Config) DefaultModel() *Model { return c.Models[c.Default] }

// validateExtraParams enforces JSON-only values and the reserved-key fence.
func validateExtraParams(where string, params map[string]any) error {
	for k := range params {
		owner, reserved := reservedParamKeys[k]
		if !reserved {
			continue
		}
		if owner == "" {
			return fmt.Errorf("%s: extra_params key %q is a reserved transport key", where, k)
		}
		return fmt.Errorf("%s: extra_params key %q is a reserved transport key — set `%s` instead",
			where, k, owner)
	}
	if _, err := json.Marshal(params); err != nil {
		return fmt.Errorf("%s: extra_params must be JSON-serializable: %w", where, err)
	}
	return nil
}

// The websearch backends.
//
// SearXNG is self-hosted, so the instance is the user's own and the tool
// inherits whatever engines and policy they already chose, with no API key and
// no third party for Strument to speak for. AnySearch is the opposite trade and
// the reason it is worth having beside it: a hosted service, nothing to run,
// working anonymously and better with a key. Exa is a third point rather than a
// second of the same: its own index rather than a federation of other engines,
// a key required, and results carrying the indexed page's own text instead of a
// search-engine snippet.
const (
	SearchSearxNG   = "searxng"
	SearchAnySearch = "anysearch"
	SearchExa       = "exa"
)

// SearchBackends lists them for help text and errors, so a typo can be answered
// with what would have worked rather than with a silent failure.
var SearchBackends = []string{SearchSearxNG, SearchAnySearch, SearchExa}

// AnySearchDefaultURL is the service's base URL. Overridable through url= so a
// mirror, or a test server, can stand in.
const AnySearchDefaultURL = "https://api.anysearch.com"

// ExaDefaultURL is Exa's API base. Overridable through url= for the same
// reasons as AnySearch's: a mirror, a gateway, or a test server.
const ExaDefaultURL = "https://api.exa.ai"

// WebSearch is a configured search backend, from search().
type WebSearch struct {
	Backend string // one of SearchBackends
	URL     string // the base URL, no trailing slash
	// APIKey authenticates a hosted backend. Empty is valid for AnySearch,
	// which serves anonymous requests at a lower rate limit, and meaningless
	// for SearXNG, which has no notion of one; Exa requires it and search()
	// refuses the backend without one. Keep it out of the config file with
	// api_key=env("..."), the way provider() does; nothing prints it, including
	// searchValue's String.
	APIKey string
	// Proxy is a socks5 URL, "direct" to opt out of a global proxy, or "" to
	// inherit it. "direct" is the case that matters: a self-hosted instance is
	// usually on localhost or the LAN, and a proxy configured for external
	// traffic has no business carrying that.
	Proxy string
}

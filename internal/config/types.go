// Package config implements Strument's Starlark configuration surface and
// the direnv-style trust gate for project configs.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// Adapters recognized by provider(). "anthropic" is reserved (deferred).
const (
	AdapterOpenAI     = "openai"
	AdapterOpenRouter = "openrouter"
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
// override them.
var reservedParamKeys = map[string]bool{
	"model":          true,
	"messages":       true,
	"stream":         true,
	"stream_options": true,
	"usage":          true,
}

// Provider is a pure carrier of endpoint + dialect; no behavior inheritance.
type Provider struct {
	Adapter     string // "openai" | "openrouter"
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
	InputCost    *llm.Money // per-token USD (config declares per-million); nil => unknown (never fabricate cost)
	OutputCost   *llm.Money
	ExtraParams  map[string]any

	// sideRef holds an unresolved string alias or inline model between
	// construction and resolution.
	sideRef any
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
	// HistoryFile overrides the chat-history path ("" => the XDG default).
	// A relative path is resolved against the project root by the caller.
	HistoryFile string
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
	// EnvSet overrides environment variables for the whole session, from the
	// `env_set` setting. Applied to Strument's own process at startup, so every
	// subprocess inherits it: git, /run, and — for names env_allow also passes —
	// the model's commands. See ApplyEnvSet.
	//
	// It does not widen what the model sees. A name set here still has to be on
	// the allowlist to reach a model-run command, which is what keeps a value
	// written in a config file from being handed to the model by accident.
	EnvSet map[string]string
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
		if reservedParamKeys[k] {
			return fmt.Errorf("%s: extra_params key %q is a reserved transport key", where, k)
		}
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
// working anonymously and better with a key.
const (
	SearchSearxNG   = "searxng"
	SearchAnySearch = "anysearch"
)

// AnySearchDefaultURL is the service's base URL. Overridable through url= so a
// mirror, or a test server, can stand in.
const AnySearchDefaultURL = "https://api.anysearch.com"

// WebSearch is a configured search backend, from search().
type WebSearch struct {
	Backend string // SearchSearxNG or SearchAnySearch
	URL     string // the base URL, no trailing slash
	// APIKey authenticates a hosted backend. Empty is valid — AnySearch serves
	// anonymous requests at a lower rate limit — and SearXNG has no notion of
	// one. Keep it out of the config file with api_key=env("..."), the way
	// provider() does; nothing prints it, including searchValue's String.
	APIKey string
	// Proxy is a socks5 URL, "direct" to opt out of a global proxy, or "" to
	// inherit it. "direct" is the case that matters: a self-hosted instance is
	// usually on localhost or the LAN, and a proxy configured for external
	// traffic has no business carrying that.
	Proxy string
}

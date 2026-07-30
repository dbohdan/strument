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

// Edit formats recognized by model(). "ask" is deliberately absent: it is
// a runtime-only format (the /ask command), not a configurable one — a
// model whose default format was "ask" could never edit anything.
var knownEditFormats = map[string]bool{
	"tool":        true,
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
	WeakModel    *Model // non-nil after resolution (self if unset)
	Reasoning    string // request-side effort: "low"/"medium"/"high"; "off" disables; "" or "default" => provider default
	ReasoningTag string // response-side inline tag to strip; "" => none
	Temperature  *float64
	RepoMap      bool
	Cache        bool // enable prompt-cache breakpoints (1h TTL) + freeze the repo map
	Context      int  // input window tokens; 0 => unknown
	MaxOutput    int
	InputCost    *llm.Money // per-token; nil => unknown (never fabricate cost)
	OutputCost   *llm.Money
	ExtraParams  map[string]any

	// weakRef holds an unresolved string alias or inline model between
	// construction and resolution.
	weakRef any
}

// ReadableName is the human-facing model name used in commit trailers: the
// configured display_name, or the slug reduced to its core — everything
// after the last "/" (dropping the provider prefix) and before the first ":"
// (dropping a ":variant" suffix, which can name a private endpoint). Falls
// back to the full slug when that reduction is empty.
func (m *Model) ReadableName() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	s := m.Slug
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return m.Slug
	}
	return s
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

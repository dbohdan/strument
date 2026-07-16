// Package config implements Strument's Starlark configuration surface and
// the direnv-style trust gate for project configs. See config-schema.md.
package config

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/dbohdan/strument/internal/llm"
)

// Adapters recognized by provider(). "anthropic" is reserved (deferred).
const (
	AdapterOpenAI     = "openai"
	AdapterOpenRouter = "openrouter"
)

// Edit formats recognized by model().
var knownEditFormats = map[string]bool{
	"diff":        true,
	"diff-fenced": true,
	"whole":       true,
}

// reservedParamKeys are transport keys Strument owns; extra_params cannot
// override them (config-schema §5).
var reservedParamKeys = map[string]bool{
	"model":          true,
	"messages":       true,
	"stream":         true,
	"stream_options": true,
	"usage":          true,
}

// Provider is a pure carrier of endpoint + dialect; no behavior inheritance
// (config-schema §3, §6).
type Provider struct {
	Adapter     string // "openai" | "openrouter"
	BaseURL     string // "" => adapter default
	APIKey      string
	Name        string
	ExtraParams map[string]any // JSON-only, reserved keys rejected
}

// GroupKey groups models onto one runtime client/connection pool per
// endpoint (config-schema §3: value semantics; grouping by adapter+base_url).
func (p Provider) GroupKey() string {
	return p.Adapter + "\x00" + p.BaseURL
}

// Model is one usable model declaration.
type Model struct {
	Provider     Provider
	Slug         string
	EditFormat   string // "diff" | "diff-fenced" | "whole"
	WeakModel    *Model // non-nil after resolution (self if unset)
	Reasoning    string // request-side effort, e.g. "low"; "" => omit
	ReasoningTag string // response-side inline tag to strip; "" => none
	Temperature  *float64
	RepoMap      bool
	Context      int // input window tokens; 0 => unknown
	MaxOutput    int
	InputCost    *llm.Money // per-token; nil => unknown (never fabricate cost)
	OutputCost   *llm.Money
	ExtraParams  map[string]any

	// weakRef holds an unresolved string alias or inline model between
	// construction and resolution.
	weakRef any
}

// RequestExtraParams merges provider-scoped and model-scoped extra_params,
// model over provider (config-schema §5).
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

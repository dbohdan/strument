package config

import (
	"errors"
	"fmt"
	"maps"

	"dbohdan.com/strument/internal/llm"
	"go.starlark.net/starlark"
)

// providerValue and modelValue are the opaque Starlark values returned by
// the constructors. Constructors are pure; env() is the sole impurity.

type providerValue struct{ p Provider }

func (v *providerValue) String() string {
	return fmt.Sprintf("provider(%q)", v.p.Adapter)
}
func (v *providerValue) Type() string          { return "provider" }
func (v *providerValue) Freeze()               {}
func (v *providerValue) Truth() starlark.Bool  { return starlark.True }
func (v *providerValue) Hash() (uint32, error) { return 0, errors.New("unhashable type: provider") }

type modelValue struct{ m *Model }

func (v *modelValue) String() string {
	return fmt.Sprintf("model(%q)", v.m.Slug)
}
func (v *modelValue) Type() string          { return "model" }
func (v *modelValue) Freeze()               {}
func (v *modelValue) Truth() starlark.Bool  { return starlark.True }
func (v *modelValue) Hash() (uint32, error) { return 0, errors.New("unhashable type: model") }

var _ starlark.HasAttrs = (*modelValue)(nil)

// modelMethods is the Starlark method table for model values.
var modelMethods = map[string]*starlark.Builtin{
	"with_extra_params": starlark.NewBuiltin("with_extra_params", modelWithExtraParams),
}

// Attr implements starlark.HasAttrs, binding methods to the receiver.
func (v *modelValue) Attr(name string) (starlark.Value, error) {
	if m, ok := modelMethods[name]; ok {
		return m.BindReceiver(v), nil
	}
	return nil, nil //nolint:nilnil // No such attribute.
}

func (v *modelValue) AttrNames() []string { return []string{"with_extra_params"} }

// starlarkToGo converts a Starlark value into a JSON-able Go value; opaque
// values (functions, providers, models) are rejected (JSON-only).
func starlarkToGo(v starlark.Value) (any, error) {
	switch v := v.(type) {
	case starlark.NoneType:
		return nil, nil //nolint:nilnil // None maps to JSON null: a valid value.
	case starlark.Bool:
		return bool(v), nil
	case starlark.Int:
		if i, ok := v.Int64(); ok {
			return i, nil
		}
		return nil, fmt.Errorf("integer too large for JSON: %s", v)
	case starlark.Float:
		return float64(v), nil
	case starlark.String:
		return string(v), nil
	case *starlark.List:
		out := make([]any, 0, v.Len())
		for e := range v.Elements() {
			g, err := starlarkToGo(e)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, 0, v.Len())
		for _, e := range v {
			g, err := starlarkToGo(e)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, v.Len())
		for k, val := range v.Entries() {
			ks, ok := starlark.AsString(k)
			if !ok {
				return nil, fmt.Errorf("dict key %s is not a string", k.String())
			}
			g, err := starlarkToGo(val)
			if err != nil {
				return nil, err
			}
			out[ks] = g
		}
		return out, nil
	default:
		return nil, fmt.Errorf("value of type %s is not JSON-serializable", v.Type())
	}
}

func dictToParams(where string, d *starlark.Dict) (map[string]any, error) {
	if d == nil || d.Len() == 0 {
		return nil, nil //nolint:nilnil // No params is a valid, empty result.
	}
	g, err := starlarkToGo(d)
	if err != nil {
		return nil, fmt.Errorf("%s: extra_params: %w", where, err)
	}
	params, ok := g.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: extra_params must be a dict", where)
	}
	if err := validateExtraParams(where, params); err != nil {
		return nil, err
	}
	return params, nil
}

// builtinProvider implements
// provider(adapter, *, base_url=None, api_key=None, name=None, proxy=None,
// extra_params={}). proxy takes a socks5:// URL, or "direct" to force a direct
// connection when a global proxy is set; unset inherits the global proxy.
func builtinProvider(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 1 {
		return nil, errors.New("provider: only 'adapter' may be positional")
	}
	var adapter, baseURL, apiKey, name, proxy string
	var extraParams *starlark.Dict
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"adapter", &adapter,
		"base_url?", &baseURL,
		"api_key?", &apiKey,
		"name?", &name,
		"proxy?", &proxy,
		"extra_params?", &extraParams,
	); err != nil {
		return nil, err
	}
	switch adapter {
	case AdapterOpenAI, AdapterOpenRouter:
	case "anthropic":
		return nil, errors.New("provider: adapter \"anthropic\" is reserved and not yet supported")
	default:
		return nil, fmt.Errorf("provider: unknown adapter %q (want \"openai\" or \"openrouter\")", adapter)
	}
	params, err := dictToParams("provider", extraParams)
	if err != nil {
		return nil, err
	}
	return &providerValue{p: Provider{
		Adapter:     adapter,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Name:        name,
		Proxy:       proxy,
		ExtraParams: params,
	}}, nil
}

// builtinModel implements
// model(provider, slug, *, display_name=None, edit_format="tool",
//
//	weak_model=None, reasoning=None, reasoning_tag=None, temperature=None,
//	repo_map=True, cache=False, context=None, max_output=None,
//	input_cost=None, output_cost=None, extra_params={}).
func builtinModel(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 2 {
		return nil, errors.New("model: only 'provider' and 'slug' may be positional")
	}
	var providerV starlark.Value
	var slug string
	var displayName string
	editFormat := "tool"
	var weakModel starlark.Value
	var reasoning, reasoningTag string
	var temperature starlark.Value
	repoMap := true
	var cache bool
	var contextTokens, maxOutput int
	var inputCost, outputCost starlark.Value
	var extraParams *starlark.Dict
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"provider", &providerV,
		"slug", &slug,
		"display_name?", &displayName,
		"edit_format?", &editFormat,
		"weak_model?", &weakModel,
		"reasoning?", &reasoning,
		"reasoning_tag?", &reasoningTag,
		"temperature?", &temperature,
		"repo_map?", &repoMap,
		"cache?", &cache,
		"context?", &contextTokens,
		"max_output?", &maxOutput,
		"input_cost?", &inputCost,
		"output_cost?", &outputCost,
		"extra_params?", &extraParams,
	); err != nil {
		return nil, err
	}

	pv, ok := providerV.(*providerValue)
	if !ok {
		return nil, fmt.Errorf("model: provider must be a provider value, got %s", providerV.Type())
	}
	if !knownEditFormats[editFormat] {
		return nil, fmt.Errorf("model: unknown edit_format %q (want \"tool\", \"diff\", \"diff-fenced\", or \"whole\")", editFormat)
	}

	m := &Model{
		Provider:     pv.p,
		Slug:         slug,
		DisplayName:  displayName,
		EditFormat:   editFormat,
		Reasoning:    reasoning,
		ReasoningTag: reasoningTag,
		RepoMap:      repoMap,
		Cache:        cache,
		Context:      contextTokens,
		MaxOutput:    maxOutput,
	}

	switch w := weakModel.(type) {
	case nil, starlark.NoneType:
		// nil ref => self, bound at resolution.
	case starlark.String:
		m.weakRef = string(w)
	case *modelValue:
		m.weakRef = w.m
	default:
		return nil, fmt.Errorf("model: weak_model must be a model value or alias string, got %s", weakModel.Type())
	}

	var err error
	if m.Temperature, err = optFloat("temperature", temperature); err != nil {
		return nil, err
	}
	var f *float64
	if f, err = optFloat("input_cost", inputCost); err != nil {
		return nil, err
	} else if f != nil {
		m.InputCost = &llm.Money{Known: true, USD: *f}
	}
	if f, err = optFloat("output_cost", outputCost); err != nil {
		return nil, err
	} else if f != nil {
		m.OutputCost = &llm.Money{Known: true, USD: *f}
	}

	if m.ExtraParams, err = dictToParams("model", extraParams); err != nil {
		return nil, err
	}
	return &modelValue{m: m}, nil
}

// modelWithExtraParams implements model.with_extra_params(**overrides): it
// returns a copy of the receiver model whose extra_params are the current
// ones (or an empty dict) overridden by the keyword arguments. The receiver
// is unchanged; the copy keeps an unresolved weak_model ref.
func modelWithExtraParams(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 0 {
		return nil, errors.New("with_extra_params: extra params must be passed by keyword")
	}
	v, ok := b.Receiver().(*modelValue)
	if !ok {
		return nil, errors.New("with_extra_params: not bound to a model value")
	}

	merged := make(map[string]any, len(v.m.ExtraParams)+len(kwargs))
	maps.Copy(merged, v.m.ExtraParams)
	for _, kw := range kwargs {
		name, ok := starlark.AsString(kw[0])
		if !ok {
			return nil, fmt.Errorf("with_extra_params: keyword name %s is not a string", kw[0].String())
		}
		g, err := starlarkToGo(kw[1])
		if err != nil {
			return nil, fmt.Errorf("with_extra_params: %s: %w", name, err)
		}
		merged[name] = g
	}
	if err := validateExtraParams("with_extra_params", merged); err != nil {
		return nil, err
	}

	copied := *v.m
	copied.ExtraParams = merged
	return &modelValue{m: &copied}, nil
}

func optFloat(name string, v starlark.Value) (*float64, error) {
	switch v := v.(type) {
	case nil, starlark.NoneType:
		return nil, nil //nolint:nilnil // Absent optional number.
	case starlark.Float:
		f := float64(v)
		return &f, nil
	case starlark.Int:
		f, _ := starlark.AsFloat(v)
		return &f, nil
	default:
		return nil, fmt.Errorf("model: %s must be a number, got %s", name, v.Type())
	}
}

// builtinEnv implements env(name, default=None, required=True) — the sole
// impurity. The lookup function is injected for tests.
func builtinEnv(lookup func(string) (string, bool)) *starlark.Builtin {
	return starlark.NewBuiltin("env", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		var def starlark.Value
		required := true
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"default?", &def,
			"required?", &required,
		); err != nil {
			return nil, err
		}
		if val, ok := lookup(name); ok {
			return starlark.String(val), nil
		}
		if required {
			return nil, fmt.Errorf("env: required environment variable %q is not set", name)
		}
		if def == nil {
			return starlark.None, nil
		}
		return def, nil
	})
}

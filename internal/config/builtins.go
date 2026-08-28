package config

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"

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
//	side_model=None, reasoning=None, reasoning_tag=None, temperature=None,
//	repo_map=True, cache=False, context=None, max_output=None,
//	input_cost=None, output_cost=None, extra_params={}).
//
// input_cost and output_cost are USD per million tokens.
func builtinModel(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 2 {
		return nil, errors.New("model: only 'provider' and 'slug' may be positional")
	}
	var providerV starlark.Value
	var slug string
	var displayName string
	editFormat := "tool"
	var sideModel starlark.Value
	var reasoning, reasoningTag string
	var temperature starlark.Value
	repoMap := true
	var cache bool
	var contextTokens, maxOutput int
	var inputCost, outputCost starlark.Value
	var extraParams *starlark.Dict
	var retiredWeakModel starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"provider", &providerV,
		"slug", &slug,
		"display_name?", &displayName,
		"edit_format?", &editFormat,
		"side_model?", &sideModel,
		"weak_model?", &retiredWeakModel,
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

	// weak_model is still unpacked so a config using it gets this sentence
	// rather than "unexpected keyword argument", which says nothing about what
	// to do next. Same reason edit_format still recognizes its retired values.
	if retiredWeakModel != nil && retiredWeakModel != starlark.None {
		return nil, errors.New(
			"model: weak_model has been renamed to side_model; rename the argument. " +
				"The old name described the model's capability, and that stopped being " +
				"true — the models used here are not weak. The new name describes its " +
				"role: it writes commit messages, session notes, and compaction " +
				"summaries, off to the side of the turn")
	}

	pv, ok := providerV.(*providerValue)
	if !ok {
		return nil, fmt.Errorf("model: provider must be a provider value, got %s", providerV.Type())
	}
	if !knownEditFormats[editFormat] {
		if retiredEditFormats[editFormat] {
			return nil, fmt.Errorf(
				"model: edit_format %q has been removed; Strument now edits through tool calls only. "+
					"Drop the setting (or use \"tool\") — a model that cannot call functions can no longer "+
					"drive Strument, since reading and searching are tool calls too", editFormat)
		}
		return nil, fmt.Errorf("model: unknown edit_format %q (the only value is \"tool\")", editFormat)
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

	switch w := sideModel.(type) {
	case nil, starlark.NoneType:
		// nil ref => self, bound at resolution.
	case starlark.String:
		m.sideRef = string(w)
	case *modelValue:
		m.sideRef = w.m
	default:
		return nil, fmt.Errorf("model: side_model must be a model value or alias string, got %s", sideModel.Type())
	}

	var err error
	if m.Temperature, err = optFloat("temperature", temperature); err != nil {
		return nil, err
	}
	// Costs are declared in US dollars per million tokens (readable: "5", not
	// "0.000005") and stored as the per-token USD the usage math expects.
	var f *float64
	if f, err = optFloat("input_cost", inputCost); err != nil {
		return nil, err
	} else if f != nil {
		m.InputCost = &llm.Money{Known: true, USD: *f / 1e6}
	}
	if f, err = optFloat("output_cost", outputCost); err != nil {
		return nil, err
	} else if f != nil {
		m.OutputCost = &llm.Money{Known: true, USD: *f / 1e6}
	}

	if m.ExtraParams, err = dictToParams("model", extraParams); err != nil {
		return nil, err
	}
	return &modelValue{m: m}, nil
}

// modelWithExtraParams implements model.with_extra_params(**overrides): it
// returns a copy of the receiver model whose extra_params are the current
// ones (or an empty dict) overridden by the keyword arguments. The receiver
// is unchanged; the copy keeps an unresolved side_model ref.
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

// builtinEnv implements env(name, default) — the sole impurity. The lookup
// function is injected for tests.
//
// Giving a default is what makes a variable optional, so there is one knob
// rather than two. It reads like Starlark's own dictionary access, which is the
// shape a config author already has: env("X") is d["x"] and raises when the key
// is absent; env("X", default=v) is d.get("x", v) and does not.
//
// There used to be a separate required=True, and default did nothing without
// required=False — so env("X", default="y"), which is what anyone would write,
// errored on a missing X and never reached the fallback. doc/config.md carried
// a paragraph headed "The gotcha worth stating outright" explaining when the
// parameter was ignored, which is the sort of documentation that should be
// taken as a bug report about the interface.
//
// Absence is the presence of the keyword, not the value: default=None means
// optional and None, distinct from omitting it. UnpackArgs leaves def nil in
// the second case only, which is the whole mechanism.
func builtinEnv(lookup func(string) (string, bool)) *starlark.Builtin {
	return starlark.NewBuiltin("env", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		var def starlark.Value
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"name", &name,
			"default?", &def,
		); err != nil {
			return nil, err
		}
		if val, ok := lookup(name); ok {
			return starlark.String(val), nil
		}
		if def == nil {
			return nil, fmt.Errorf(
				"env: environment variable %q is not set; pass a default to make it optional, "+
					"e.g. env(%q, default = \"\")", name, name)
		}
		return def, nil
	})
}

// searchValue is the opaque Starlark value returned by search().
type searchValue struct{ s WebSearch }

func (v *searchValue) String() string {
	return fmt.Sprintf("search(%q)", v.s.Backend)
}
func (v *searchValue) Type() string          { return "search" }
func (v *searchValue) Freeze()               {}
func (v *searchValue) Truth() starlark.Bool  { return starlark.True }
func (v *searchValue) Hash() (uint32, error) { return 0, errors.New("unhashable type: search") }

// builtinSearch implements search(backend, *, url=None, proxy=None).
//
// The backend is a string rather than one builtin per backend — search("searxng")
// rather than searxng() — for the reason provider() takes one: the backends share
// a shape, so a discriminator says so, adding one stays a table entry rather than
// a new name in the DSL, and a typo can be answered with the list of what would
// have worked. "undefined: searxngg" cannot say that.
//
// url is keyword-only even though searxng always needs it, because the next
// backend will want an api_key and no url, and a positional second argument that
// means a different thing per backend is a trap.
func builtinSearch(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) > 1 {
		return nil, errors.New("search: only 'backend' may be positional")
	}
	var backend, rawURL, proxy string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"backend", &backend,
		"url?", &rawURL,
		"proxy?", &proxy,
	); err != nil {
		return nil, err
	}
	if backend != SearchSearxNG {
		return nil, fmt.Errorf("search: unknown backend %q (want %q)", backend, SearchSearxNG)
	}
	// Checked here rather than at the first search, because a config error that
	// waits for the model to reach for a tool is a config error nobody sees.
	u, err := url.Parse(strings.TrimSpace(rawURL))
	switch {
	case strings.TrimSpace(rawURL) == "":
		return nil, fmt.Errorf("search: %q needs url=, the base URL of your instance", backend)
	case err != nil:
		return nil, fmt.Errorf("search: url %q: %w", rawURL, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, fmt.Errorf("search: url %q needs an http:// or https:// scheme", rawURL)
	case u.Host == "":
		return nil, fmt.Errorf("search: url %q has no host", rawURL)
	case u.RawQuery != "" || u.Fragment != "":
		return nil, fmt.Errorf("search: url %q should be the instance's base URL, with no query or fragment", rawURL)
	}
	return &searchValue{s: WebSearch{
		Backend: backend,
		// Trailing slash trimmed so joining "/search" cannot produce "//search",
		// which some reverse proxies in front of an instance will not route.
		URL:   strings.TrimRight(u.String(), "/"),
		Proxy: proxy,
	}}, nil
}

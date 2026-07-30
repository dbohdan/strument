package config

import (
	"strings"
	"testing"
)

// TestProxyResolution: the global proxy is the fallback; a provider's own proxy
// overrides it; "direct" forces a direct connection. Resolution runs once per
// distinct *Model, so an alias-dup and an inline weak model keep the right
// value even though the "direct"->inherit rewrite is not idempotent.
func TestProxyResolution(t *testing.T) {
	src := `
direct_prov = provider("openai", base_url = "http://localhost:8000/v1", proxy = "direct")
orouter = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
own = provider("openrouter", api_key = env("OPENROUTER_API_KEY"), proxy = "socks5://other:1080")

models = {
    "inherit": model(orouter, "openrouter/x"),
    "own": model(own, "openrouter/y"),
    "d1": model(direct_prov, "local/z", weak_model = model(direct_prov, "local/w")),
    "d2": None,
}
models["d2"] = models["d1"]

proxy = "socks5://global:1080"
default = "inherit"
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"inherit": "socks5://global:1080", // unset -> global fallback
		"own":     "socks5://other:1080",  // own proxy wins
		"d1":      "",                     // "direct" -> direct connection
		"d2":      "",                     // alias-dup of d1: not re-inherited
	}
	for alias, want := range cases {
		if got := cfg.Models[alias].Provider.Proxy; got != want {
			t.Errorf("model %q proxy = %q, want %q", alias, got, want)
		}
	}
	if got := cfg.Proxy; got != "socks5://global:1080" {
		t.Errorf("cfg.Proxy = %q", got)
	}
	// The inline weak model of a "direct" provider also resolves to direct.
	if got := cfg.Models["d1"].WeakModel.Provider.Proxy; got != "" {
		t.Errorf("d1 weak proxy = %q, want direct (empty)", got)
	}
}

func TestProxyRejectsBadScheme(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"), proxy = "http://nope:8080")
models = {"m": model(p, "x")}
default = "m"
`
	if _, err := Load(harness(t, src, "", testEnv)); err == nil || !strings.Contains(err.Error(), "unsupported scheme") {
		t.Fatalf("want unsupported-scheme error, got %v", err)
	}
}

func TestProxyRejectsGlobalDirect(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "x")}
proxy = "direct"
default = "m"
`
	if _, err := Load(harness(t, src, "", testEnv)); err == nil || !strings.Contains(err.Error(), "cannot be") {
		t.Fatalf("want global-direct rejection, got %v", err)
	}
}

package config

import "testing"

func TestCacheOptionParsed(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {
    "cached": model(p, "anthropic/claude", cache = True),
    "plain": model(p, "openai/gpt"),
}
default = "cached"
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Models["cached"].Cache {
		t.Error("cache = True did not set Model.Cache")
	}
	if cfg.Models["plain"].Cache {
		t.Error("Model.Cache must default to false")
	}
}

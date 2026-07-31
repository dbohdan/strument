package modelconfig

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// TestEmitLoadsBackAndPinsSchema is the anti-drift guarantee: an emitted block,
// wrapped in a minimal config, must parse through config.Load and land in the
// right config.Model fields. Rename a model() parameter in the builtin and this
// goes red — the emitter can't silently drift from the schema it feeds.
func TestEmitLoadsBackAndPinsSchema(t *testing.T) {
	info := ModelInfo{
		Slug:         "anthropic/claude-haiku-4.5",
		DisplayName:  "Claude Haiku 4.5",
		Context:      200000,
		MaxOutput:    64000,
		InputCost:    "1", // per million tokens; loads back as 0.000001 per token
		OutputCost:   "5",
		CacheCapable: true,
		Reasoning:    true,
	}
	// EmitStarlark already produces a full `models = {...}`, keyed by the slug
	// core; it just needs a provider binding and a default.
	block := EmitStarlark([]ModelInfo{info}, "openrouter")

	src := `openrouter = provider("openrouter", api_key="x")
` + block + `default = "claude-haiku-4.5"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.star")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(config.Options{UserConfigPath: path})
	if err != nil {
		t.Fatalf("emitted block did not load (schema drift?): %v\n%s", err, src)
	}
	m := cfg.Models["claude-haiku-4.5"]
	if m == nil {
		t.Fatal("model alias \"claude-haiku-4.5\" missing after load")
	}
	if m.Context != 200000 {
		t.Errorf("context = %d, want 200000", m.Context)
	}
	if m.MaxOutput != 64000 {
		t.Errorf("max_output = %d, want 64000", m.MaxOutput)
	}
	if m.DisplayName != "Claude Haiku 4.5" {
		t.Errorf("display_name = %q", m.DisplayName)
	}
	if !m.Cache {
		t.Error("cache should be true")
	}
	if m.InputCost == nil || math.Abs(m.InputCost.USD-0.000001) > 1e-12 {
		t.Errorf("input_cost = %v, want 0.000001", m.InputCost)
	}
	if m.OutputCost == nil || math.Abs(m.OutputCost.USD-0.000005) > 1e-12 {
		t.Errorf("output_cost = %v, want 0.000005", m.OutputCost)
	}
}

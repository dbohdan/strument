package modelconfig

import (
	"math"
	"os"
	"path/filepath"
	"strings"
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
		// Emitted as a list and loaded back through parseModalities, so a
		// rename on either side goes red here rather than in a session.
		InputModalities: []string{"text", "image"},
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

// The emitted modality list has to survive the trip, because the generator is
// how most users will ever get one: OpenRouter reported image support for 274
// of 445 models on 2026-09-14, and nobody is going to hand-write that.
func TestEmitCarriesInputModalities(t *testing.T) {
	block := EmitStarlark([]ModelInfo{{
		Slug:            "vendor/sees",
		DisplayName:     "Sees",
		InputModalities: []string{"text", "image"},
	}}, "openrouter")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.star")
	src := `openrouter = provider("openrouter", api_key="x")
` + block + `default = "sees"
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{UserConfigPath: path})
	if err != nil {
		t.Fatalf("emitted config did not load: %v\n%s", err, src)
	}
	if !cfg.Models["sees"].Accepts("image") {
		t.Errorf("input_modalities did not survive the round trip:\n%s", src)
	}
}

// A text-only model must not get a line at all. Every model in a generated
// file would otherwise carry a redundant declaration of the default.
func TestTextOnlyModelEmitsNoModalityLine(t *testing.T) {
	block := EmitStarlark([]ModelInfo{{Slug: "vendor/plain", DisplayName: "Plain"}}, "openrouter")
	if strings.Contains(block, "input_modalities") {
		t.Errorf("a text-only model emitted a modality line:\n%s", block)
	}
}

// And the narrowing itself, against the values OpenRouter actually reports.
func TestCarriableModalitiesNarrowsToWhatWeCanSend(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reported []string
		want     []string
	}{
		{"text only", []string{"text"}, nil},
		{"nothing reported", nil, nil},
		{"image kept", []string{"text", "image"}, []string{"text", "image"}},
		{"file audio video dropped", []string{"text", "image", "file", "audio", "video"}, []string{"text", "image"}},
		{"image only, no text", []string{"image"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := carriableModalities(tc.reported)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

package modelconfig

import (
	"strings"
	"testing"
)

func TestEmitStarlarkFull(t *testing.T) {
	info := ModelInfo{
		Slug:         "anthropic/claude-haiku-4.5",
		DisplayName:  "Claude Haiku 4.5",
		Context:      200000,
		MaxOutput:    64000,
		InputCost:    "1",
		OutputCost:   "5",
		CacheCapable: true,
		Reasoning:    true,
	}
	want := `models = {
    "claude-haiku-4.5": model(
        openrouter,
        "anthropic/claude-haiku-4.5",
        display_name="Claude Haiku 4.5",
        context=200000,
        max_output=64000,
        input_cost=1,
        output_cost=5,
        cache=True,  # OpenRouter reports prompt caching for this model.
        # reasoning="low",  # Uncomment and set the effort: "low", "medium", or "high".
        # reasoning_tag="think",  # Uncomment if the model emits reasoning in inline tags.
        # side_model="...",  # Uncomment to use a cheaper model for summaries and commits.
    ),
}
`
	if got := EmitStarlark([]ModelInfo{info}, "openrouter"); got != want {
		t.Errorf("EmitStarlark mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEmitStarlarkPlain: no cache, no reasoning, unknown max output. A known
// zero cost ("0") is still emitted; only unknown (empty) costs are omitted. The
// alias is the slug core.
func TestEmitStarlarkPlain(t *testing.T) {
	info := ModelInfo{
		Slug:        "vendor/plain",
		DisplayName: "Plain",
		Context:     32768,
		InputCost:   "0",
		OutputCost:  "0",
	}
	want := `models = {
    "plain": model(
        local,
        "vendor/plain",
        display_name="Plain",
        context=32768,
        input_cost=0,
        output_cost=0,
        # side_model="...",  # Uncomment to use a cheaper model for summaries and commits.
    ),
}
`
	if got := EmitStarlark([]ModelInfo{info}, "local"); got != want {
		t.Errorf("EmitStarlark mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEmitStarlarkDedup: two slugs that reduce to the same core get distinct
// keys, so a collision never silently drops a model.
func TestEmitStarlarkDedup(t *testing.T) {
	infos := []ModelInfo{
		{Slug: "x/foo:a", Context: 1},
		{Slug: "y/foo:b", Context: 2},
	}
	got := EmitStarlark(infos, "openrouter")
	for _, key := range []string{`"foo": model(`, `"foo-2": model(`} {
		if !strings.Contains(got, key) {
			t.Errorf("output missing %q:\n%s", key, got)
		}
	}
}

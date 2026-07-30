package modelconfig

import "testing"

func TestEmitStarlarkFull(t *testing.T) {
	info := ModelInfo{
		Slug:         "anthropic/claude-haiku-4.5",
		DisplayName:  "Claude Haiku 4.5",
		Context:      200000,
		MaxOutput:    64000,
		InputCost:    "0.000001",
		OutputCost:   "0.000005",
		CacheCapable: true,
		Reasoning:    true,
	}
	want := `model(
    openrouter,
    "anthropic/claude-haiku-4.5",
    display_name="Claude Haiku 4.5",
    context=200000,
    max_output=64000,
    input_cost=0.000001,
    output_cost=0.000005,
    cache=True,  # OpenRouter reports prompt caching for this model.
    # reasoning="low",  # Uncomment and set the effort: "low", "medium", or "high".
    # reasoning_tag="think",  # Uncomment if the model emits reasoning in inline tags.
    # weak_model="...",  # Uncomment to use a cheaper model for summaries and commits.
),
`
	if got := EmitStarlark(info, "openrouter"); got != want {
		t.Errorf("EmitStarlark mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEmitStarlarkPlain: no cache, no reasoning, unknown max output. A known
// zero cost ("0") is still emitted; only unknown (empty) costs are omitted.
func TestEmitStarlarkPlain(t *testing.T) {
	info := ModelInfo{
		Slug:        "vendor/plain",
		DisplayName: "Plain",
		Context:     32768,
		InputCost:   "0",
		OutputCost:  "0",
	}
	want := `model(
    local,
    "vendor/plain",
    display_name="Plain",
    context=32768,
    input_cost=0,
    output_cost=0,
    # weak_model="...",  # Uncomment to use a cheaper model for summaries and commits.
),
`
	if got := EmitStarlark(info, "local"); got != want {
		t.Errorf("EmitStarlark mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

package modelconfig

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// cannedModels covers three shapes: an explicit cacher (read+write price), an
// implicit cacher (read price, null write — like OpenAI), and a plain model
// (no cache, no reasoning, null max_completion_tokens).
const cannedModels = `{"data":[
  {"id":"vendor/cacher","name":"Vendor: Cacher X","context_length":200000,
   "top_provider":{"max_completion_tokens":64000},
   "pricing":{"prompt":"0.000001","completion":"0.000005","input_cache_read":"0.0000001","input_cache_write":"0.00000125"},
   "supported_parameters":["tools","reasoning"]},
  {"id":"vendor/implicit","name":"Vendor: Implicit","context_length":128000,
   "top_provider":{"max_completion_tokens":16000},
   "pricing":{"prompt":"0.000002","completion":"0.000008","input_cache_read":"0.0000002","input_cache_write":null},
   "supported_parameters":["reasoning"]},
  {"id":"vendor/plain","name":"Plain Local Model","context_length":32768,
   "top_provider":{"max_completion_tokens":null},
   "pricing":{"prompt":"0","completion":"0","input_cache_read":null},
   "supported_parameters":["tools"]}
]}`

func cannedSource() *OpenRouterSource {
	return &OpenRouterSource{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != openRouterModelsURL {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("no"))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(cannedModels)),
			Header:     make(http.Header),
		}, nil
	})}
}

func TestOpenRouterSourceLookup(t *testing.T) {
	found, missing, err := cannedSource().Lookup([]string{"vendor/cacher", "vendor/implicit", "vendor/plain", "vendor/nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "vendor/nope" {
		t.Errorf("missing = %v, want [vendor/nope]", missing)
	}
	if len(found) != 3 {
		t.Fatalf("found %d models, want 3", len(found))
	}

	cacher := found[0]
	if cacher.DisplayName != "Cacher X" {
		t.Errorf("display name = %q, want %q (Vendor: prefix stripped)", cacher.DisplayName, "Cacher X")
	}
	if cacher.Context != 200000 || cacher.MaxOutput != 64000 {
		t.Errorf("cacher context/max = %d/%d, want 200000/64000", cacher.Context, cacher.MaxOutput)
	}
	if cacher.InputCost != "0.000001" || cacher.OutputCost != "0.000005" {
		t.Errorf("cacher costs = %q/%q", cacher.InputCost, cacher.OutputCost)
	}
	if !cacher.CacheCapable || !cacher.Reasoning {
		t.Errorf("cacher cache/reasoning = %v/%v, want true/true", cacher.CacheCapable, cacher.Reasoning)
	}

	// Implicit cacher: null write price but a read price still means caching.
	if !found[1].CacheCapable {
		t.Error("implicit cacher (read price, null write) should be CacheCapable")
	}

	plain := found[2]
	if plain.DisplayName != "Plain Local Model" {
		t.Errorf("plain display name = %q (no colon to strip)", plain.DisplayName)
	}
	if plain.MaxOutput != 0 {
		t.Errorf("plain max_output = %d, want 0 (null)", plain.MaxOutput)
	}
	if plain.InputCost != "0" {
		t.Errorf("plain input_cost = %q, want %q (a real, known zero)", plain.InputCost, "0")
	}
	if plain.CacheCapable || plain.Reasoning {
		t.Errorf("plain cache/reasoning = %v/%v, want false/false", plain.CacheCapable, plain.Reasoning)
	}
}

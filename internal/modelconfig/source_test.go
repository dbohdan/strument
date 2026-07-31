package modelconfig

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

// cannedSource serves the fixture catalog and caches into a temp dir so tests
// never touch the real XDG cache.
func cannedSource(t *testing.T) *OpenRouterSource {
	t.Helper()
	return &OpenRouterSource{
		APIKey:   "test-key",
		CacheDir: t.TempDir(),
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != openRouterModelsURL {
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("no"))}, nil
			}
			return okResp(), nil
		}),
	}
}

func okResp() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(cannedModels)),
		Header:     make(http.Header),
	}
}

// TestPerMillion pins the exact decimal-shift scaling — the reason it isn't a
// float multiply is that 0.000005*1e6 formats as 5.000000000000001 in float64.
func TestPerMillion(t *testing.T) {
	cases := map[string]string{
		"0.000005":   "5",
		"0.000001":   "1",
		"0.0000006":  "0.6",
		"0.00000015": "0.15",
		"0.00000105": "1.05",
		"0":          "0",
		"0.0":        "0",
		"1.5":        "1500000",
		"":           "",
		"abc":        "",
		"1e-6":       "", // scientific notation is not a plain decimal we shift
	}
	for in, want := range cases {
		if got := perMillion(in); got != want {
			t.Errorf("perMillion(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFetchSendsAuthAndIdentity: the request must authenticate and identify
// itself, so OpenRouter's Cloudflare layer doesn't read it as an anonymous
// scraper and IP-block it.
func TestFetchSendsAuthAndIdentity(t *testing.T) {
	var got *http.Request
	src := &OpenRouterSource{
		APIKey:    "sk-test",
		UserAgent: "Strument/9.9.9",
		CacheDir:  t.TempDir(),
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			got = r
			return okResp(), nil
		}),
	}
	if _, _, err := src.Lookup([]string{"vendor/cacher"}); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no request captured")
	}
	for header, want := range map[string]string{
		"Authorization": "Bearer sk-test",
		"Http-Referer":  appReferer,
		"X-Title":       appTitle,
		"User-Agent":    "Strument/9.9.9",
	} {
		if h := got.Header.Get(header); h != want {
			t.Errorf("%s = %q, want %q", header, h, want)
		}
	}
}

// TestCacheAvoidsSecondFetch: a second lookup of a cached model makes no request.
func TestCacheAvoidsSecondFetch(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	mk := func() *OpenRouterSource {
		return &OpenRouterSource{
			APIKey:   "k",
			CacheDir: dir,
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				return okResp(), nil
			}),
		}
	}
	if _, _, err := mk().Lookup([]string{"vendor/cacher"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mk().Lookup([]string{"vendor/cacher"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 (second lookup should hit the cache)", calls)
	}
}

// TestCacheExpiresAfterTTL: past the TTL, the entry is refetched.
func TestCacheExpiresAfterTTL(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	now := time.Now()
	mk := func(at time.Time) *OpenRouterSource {
		return &OpenRouterSource{
			APIKey:   "k",
			CacheDir: dir,
			Now:      func() time.Time { return at },
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				return okResp(), nil
			}),
		}
	}
	if _, _, err := mk(now).Lookup([]string{"vendor/cacher"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mk(now.Add(cacheTTL + time.Minute)).Lookup([]string{"vendor/cacher"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want 2 (entry should expire after the TTL)", calls)
	}
}

func TestOpenRouterSourceLookup(t *testing.T) {
	found, missing, err := cannedSource(t).Lookup([]string{"vendor/cacher", "vendor/implicit", "vendor/plain", "vendor/nope"})
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
	// Per-token 0.000001/0.000005 scaled to per-million 1/5.
	if cacher.InputCost != "1" || cacher.OutputCost != "5" {
		t.Errorf("cacher costs = %q/%q, want 1/5", cacher.InputCost, cacher.OutputCost)
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

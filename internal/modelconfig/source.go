// Package modelconfig scaffolds Starlark model() blocks from a provider's model
// metadata (OpenRouter today). It backs the `strument model-config` command: a
// lens onto someone else's live catalog that emits copy-pastable config, so the
// user never hand-looks-up the tedious fields (context size, costs, cache
// capability). It maintains no database of its own — the catalog is fetched on
// demand and frozen into user-owned config.
package modelconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ModelInfo is the provider-neutral metadata the emitter renders. Cost fields
// are per-million-token USD strings — the provider's per-token price scaled up
// (x1,000,000) for readability, as an exact decimal shift so no float round-trip
// mangles the value (empty => unknown). config divides them back by 1e6 at load.
type ModelInfo struct {
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	Context      int    `json:"context"`
	MaxOutput    int    `json:"max_output"` // 0 => unknown
	InputCost    string `json:"input_cost"`
	OutputCost   string `json:"output_cost"`
	CacheCapable bool   `json:"cache_capable"`
	Reasoning    bool   `json:"reasoning"`
}

// Source resolves exact model slugs to ModelInfo. Missing slugs are returned
// separately so the caller can report them without aborting the batch.
type Source interface {
	Lookup(slugs []string) (found []ModelInfo, missing []string, err error)
}

const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

// OpenRouter app-attribution headers, mirroring internal/client: an
// authenticated, identified request reads as a known app rather than an
// anonymous scraper that Cloudflare's bot protection may IP-block.
const (
	appReferer = "https://dbohdan.com/strument"
	appTitle   = "Strument"
)

// cacheTTL bounds how long a cached model entry is served before a refetch. The
// catalog moves slowly and the runtime cost line prefers the provider's in-band
// cost anyway, so a day keeps a working session's repeated runs to one request
// without meaningfully staling the output.
const cacheTTL = 24 * time.Hour

// OpenRouterSource reads OpenRouter's /models catalog. APIKey authenticates the
// request — anonymous catalog requests are rate-limited and can get the IP
// blocked, so it is required in practice. Transport, CacheDir, and Now are
// injection seams so the test suite never opens a socket or touches the real
// cache.
type OpenRouterSource struct {
	APIKey    string
	UserAgent string // e.g. "Strument/1.2.3"; "" => the transport default
	Transport http.RoundTripper
	CacheDir  string           // "" => os.UserCacheDir()/strument/model-config
	Now       func() time.Time // nil => time.Now
}

func (s *OpenRouterSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Lookup resolves each slug from the per-model cache, fetching the catalog once
// (and only) when some requested slug is uncached or stale.
func (s *OpenRouterSource) Lookup(slugs []string) (found []ModelInfo, missing []string, err error) {
	now := s.now()
	result := make(map[string]ModelInfo, len(slugs))
	var toFetch []string
	for _, slug := range slugs {
		if info, ok := s.readCache(slug, now); ok {
			result[slug] = info
		} else {
			toFetch = append(toFetch, slug)
		}
	}

	if len(toFetch) > 0 {
		models, err := s.fetch()
		if err != nil {
			return nil, nil, err
		}
		byID := make(map[string]orModel, len(models))
		for _, m := range models {
			byID[m.ID] = m
		}
		for _, slug := range toFetch {
			if m, ok := byID[slug]; ok {
				info := toInfo(m)
				result[slug] = info
				s.writeCache(slug, info, now)
			}
		}
	}

	for _, slug := range slugs {
		if info, ok := result[slug]; ok {
			found = append(found, info)
		} else {
			missing = append(missing, slug)
		}
	}
	return found, missing, nil
}

func (s *OpenRouterSource) fetch() ([]orModel, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, openRouterModelsURL, nil)
	if err != nil {
		return nil, err
	}
	// Authenticate and identify: an anonymous request with Go's default
	// user-agent reads as a scraper to OpenRouter's Cloudflare layer.
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	// OpenRouter's docs write "HTTP-Referer"; Go canonicalizes to "Http-Referer"
	// and header names are case-insensitive, so it matches (as in internal/client).
	req.Header.Set("Http-Referer", appReferer)
	req.Header.Set("X-Title", appTitle)
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}

	httpClient := &http.Client{Transport: s.Transport, Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching OpenRouter models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter models: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []orModel `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parsing OpenRouter models: %w", err)
	}
	return parsed.Data, nil
}

// cacheEntry is one cached model, with its fetch time for the TTL check.
type cacheEntry struct {
	Info      ModelInfo `json:"info"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (s *OpenRouterSource) cachePath(slug string) string {
	dir := s.CacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "" // no resolvable cache dir => caching disabled
		}
		dir = filepath.Join(base, "strument", "model-config")
	}
	return filepath.Join(dir, url.QueryEscape(slug)+".json")
}

// readCache returns a fresh cached entry, if any. Any problem (missing, corrupt,
// stale) is a miss: the cache never fails a lookup, it only saves a request.
func (s *OpenRouterSource) readCache(slug string, now time.Time) (ModelInfo, bool) {
	path := s.cachePath(slug)
	if path == "" {
		return ModelInfo{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ModelInfo{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return ModelInfo{}, false
	}
	if now.Sub(e.FetchedAt) > cacheTTL {
		return ModelInfo{}, false
	}
	return e.Info, true
}

// writeCache stores one model entry, best-effort — a cache write never fails a
// lookup.
func (s *OpenRouterSource) writeCache(slug string, info ModelInfo, now time.Time) {
	path := s.cachePath(slug)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(cacheEntry{Info: info, FetchedAt: now})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// orModel is the subset of OpenRouter's /models entry we read.
type orModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	TopProvider   struct {
		MaxCompletionTokens *int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Pricing struct {
		Prompt         string `json:"prompt"`
		Completion     string `json:"completion"`
		InputCacheRead string `json:"input_cache_read"`
	} `json:"pricing"`
	SupportedParameters []string `json:"supported_parameters"`
}

func toInfo(m orModel) ModelInfo {
	info := ModelInfo{
		Slug:        m.ID,
		DisplayName: cleanName(m.Name),
		Context:     m.ContextLength,
		InputCost:   perMillion(m.Pricing.Prompt),
		OutputCost:  perMillion(m.Pricing.Completion),
	}
	if m.TopProvider.MaxCompletionTokens != nil {
		info.MaxOutput = *m.TopProvider.MaxCompletionTokens
	}
	// A non-zero cache-READ price is the universal "this provider caches" tell.
	// Implicit cachers (e.g. OpenAI) leave input_cache_write null but still list
	// a read price, and cache=True still earns its keep there, because the
	// prompt prefix is stable across turns for their automatic caching to key
	// on.
	info.CacheCapable = isPositivePrice(m.Pricing.InputCacheRead)
	info.Reasoning = slices.Contains(m.SupportedParameters, "reasoning")
	return info
}

// cleanName drops OpenRouter's "Vendor: " prefix ("Anthropic: Claude Haiku 4.5"
// => "Claude Haiku 4.5").
func cleanName(name string) string {
	if _, after, found := strings.Cut(name, ": "); found {
		return after
	}
	return name
}

// perMillion multiplies a plain decimal price string by 1,000,000 exactly, by
// shifting the decimal point six places right: OpenRouter quotes per-token
// prices ("0.000005"), and per-million-token ("5") is what people read. The
// shift is string-only so no float round-trip mangles the value (0.000005*1e6
// formats as 5.000000000000001 in float64). Returns "" for an empty or
// non-plain-decimal string (unknown => omit); "0" stays "0", a real known price.
func perMillion(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, fracPart, _ := strings.Cut(s, ".")
	if (intPart == "" && fracPart == "") || !allDigits(intPart) || !allDigits(fracPart) {
		return ""
	}

	const shift = 6
	var out string
	if len(fracPart) <= shift {
		digits := strings.TrimLeft(intPart+fracPart+strings.Repeat("0", shift-len(fracPart)), "0")
		if digits == "" {
			digits = "0"
		}
		out = digits
	} else {
		whole := strings.TrimLeft(intPart+fracPart[:shift], "0")
		if whole == "" {
			whole = "0"
		}
		if frac := strings.TrimRight(fracPart[shift:], "0"); frac != "" {
			out = whole + "." + frac
		} else {
			out = whole
		}
	}
	if neg && out != "0" {
		out = "-" + out
	}
	return out
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isPositivePrice(s string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && f > 0
}

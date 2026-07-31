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
	Slug         string
	DisplayName  string
	Context      int
	MaxOutput    int // 0 => unknown
	InputCost    string
	OutputCost   string
	CacheCapable bool
	Reasoning    bool
}

// Source resolves exact model slugs to ModelInfo. Missing slugs are returned
// separately so the caller can report them without aborting the batch.
type Source interface {
	Lookup(slugs []string) (found []ModelInfo, missing []string, err error)
}

const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

// OpenRouterSource reads OpenRouter's public /models catalog. Transport is an
// injection seam (nil => the default), mirroring client.Client so the test
// suite never opens a socket.
type OpenRouterSource struct {
	Transport http.RoundTripper
}

func (s *OpenRouterSource) Lookup(slugs []string) (found []ModelInfo, missing []string, err error) {
	models, err := s.fetch()
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]orModel, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}
	for _, slug := range slugs {
		m, ok := byID[slug]
		if !ok {
			missing = append(missing, slug)
			continue
		}
		found = append(found, toInfo(m))
	}
	return found, missing, nil
}

func (s *OpenRouterSource) fetch() ([]orModel, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, openRouterModelsURL, nil)
	if err != nil {
		return nil, err
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
	// a read price, and cache=True still earns its keep there — the frozen repo
	// map stabilizes the prefix their automatic caching keys on.
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

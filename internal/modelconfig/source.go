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
// are the provider's per-token USD strings, kept verbatim (empty => unknown);
// preserving the string avoids any float round-trip surprise in the output.
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
		InputCost:   validCost(m.Pricing.Prompt),
		OutputCost:  validCost(m.Pricing.Completion),
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

// validCost returns the price string when it parses as a number, else ""
// (unknown => omit). "0" is a real, known price and is kept.
func validCost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return ""
	}
	return s
}

func isPositivePrice(s string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && f > 0
}

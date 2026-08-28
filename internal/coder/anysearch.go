package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Searching through AnySearch, the hosted backend beside SearXNG.
//
// The two are opposite trades, which is why both are worth having. A SearXNG
// instance is the user's own: their engines, their policy, nothing to trust and
// nothing to sign up for, but something to run. AnySearch is a service: nothing
// to run, works with no key at all, better with one — and a third party sees
// every query. Neither is the right answer for everyone, so the config names
// which.
//
// The shape below was read off a live service, not the vendor's install guide,
// which documents a CLI wrapper rather than the HTTP API underneath it.

const (
	anySearchTimeout = 30 * time.Second
	// The service's own cap. Asking for more is an error rather than a
	// courtesy, so the request is clamped rather than sent hopefully.
	anySearchMaxResults = 10
)

// anySearchResponse is the JSON envelope, decoding only what is used.
//
// Every response carries code and message, and a failure can arrive with an
// HTTP status that agrees or one that does not — the vendor's own client checks
// both, and so does this. request_id is kept because it is the only handle a
// user has when asking the service about a failed query.
type anySearchResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Data      struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			// Both are sent and are usually identical. content is the fuller of
			// the two where they differ, so it wins and snippet is the fallback.
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	} `json:"data"`
}

// NewAnySearch returns a Searcher backed by the AnySearch service. apiKey may
// be empty: the service answers anonymously at a lower rate limit, which is
// worth keeping usable — a search tool that demands a signup before it does
// anything is one most people never turn on.
func NewAnySearch(baseURL, apiKey string, transport http.RoundTripper, userAgent string) Searcher {
	if userAgent == "" {
		userAgent = scrapeUserAgentDefault
	}
	client := &http.Client{Transport: transport, Timeout: anySearchTimeout}
	return func(ctx context.Context, query string) (SearchResults, error) {
		body, err := json.Marshal(map[string]any{
			"query":       query,
			"max_results": anySearchMaxResults,
		})
		if err != nil {
			return SearchResults{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/search",
			bytes.NewReader(body))
		if err != nil {
			return SearchResults{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		// Strument identifies as itself. The vendor's CLI sends a client header
		// of its own; sending that would be claiming to be their client, which
		// is not true and is not ours to claim.
		req.Header.Set("User-Agent", userAgent)
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			return SearchResults{}, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, searchMaxBytes))
		if err != nil {
			return SearchResults{}, err
		}

		var out anySearchResponse
		if jsonErr := json.Unmarshal(raw, &out); jsonErr != nil {
			// The status first, because a proxy or a captive portal answering
			// instead of the service is the likelier cause of unparseable bytes
			// than the service having changed its format.
			if resp.StatusCode != http.StatusOK {
				return SearchResults{}, fmt.Errorf("HTTP %d from the service", resp.StatusCode)
			}
			return SearchResults{}, fmt.Errorf("the service did not return valid JSON: %w", jsonErr)
		}
		if err := anySearchError(resp.StatusCode, out); err != nil {
			return SearchResults{}, err
		}
		return anySearchToResults(query, out), nil
	}
}

// anySearchError turns a failure into something a user can act on. The service
// reports one in two places at once, and neither alone is enough: a bad key
// arrives as HTTP 401 *and* code -1, while the vendor's own client treats a
// non-zero code as fatal whatever the status says.
func anySearchError(status int, out anySearchResponse) error {
	if status == http.StatusOK && out.Code == 0 {
		return nil
	}
	msg := strings.TrimSpace(out.Message)
	if msg == "" || msg == "success" {
		msg = fmt.Sprintf("HTTP %d", status)
	}
	// Named rather than left as "Invalid API key.": the user is the one who can
	// fix it, and the fix differs from every other failure here.
	if status == http.StatusUnauthorized || strings.Contains(strings.ToLower(msg), "api key") {
		return fmt.Errorf("the service rejected the API key (%s). Check api_key= on search(), "+
			"or drop it — AnySearch also answers anonymously, at a lower rate limit", msg)
	}
	if status == http.StatusTooManyRequests {
		return errors.New("the service is rate-limiting this key (HTTP 429). An anonymous " +
			"caller has a lower limit than a key, so this may be the anonymous one")
	}
	if out.RequestID != "" {
		return fmt.Errorf("%s (request %s)", msg, out.RequestID)
	}
	return errors.New(msg)
}

// anySearchToResults reduces a decoded response. AnySearch reports no
// per-engine health, so Unresponsive stays empty — the degraded-search note is
// SearXNG's, and inventing one here would claim knowledge the service does not
// give. An empty result set is simply empty.
func anySearchToResults(query string, out anySearchResponse) SearchResults {
	res := SearchResults{Query: query}
	for _, r := range out.Data.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		if len(res.Results) == searchMaxResults {
			break
		}
		content := r.Content
		if strings.TrimSpace(content) == "" {
			content = r.Snippet
		}
		res.Results = append(res.Results, SearchResult{
			Title:   clipRunes(strings.TrimSpace(r.Title), searchMaxSnippet),
			URL:     strings.TrimSpace(r.URL),
			Content: clipRunes(strings.TrimSpace(content), searchMaxSnippet),
		})
	}
	return res
}

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

// Searching through Exa, the third backend beside SearXNG and AnySearch.
//
// Exa runs its own index rather than federating other engines, and returns the
// indexed page's own text instead of a SERP snippet — so a result here carries
// the opening of the page rather than the fragment that matched. That is the
// trade worth knowing: the text is more substantial than a snippet and less
// targeted, because it is the top of the document rather than the part about
// the query. Exa's `highlights` would be query-relevant, but it is billed extra
// and runs a model to produce them, so the cheap deterministic field is the one
// used here.
//
// It needs a key; there is no anonymous tier. What there is instead is an x402
// payment challenge, which is why exaError names the missing key rather than
// letting HTTP 402 reach the user as-is.

const (
	exaTimeout = 30 * time.Second
	// The mode. Not the default, and this is the whole reason the constant
	// exists rather than being left unset.
	//
	// Exa's default is `auto`, and `auto` returns a bare origin in place of the
	// page's URL often enough to matter: over 45 developer queries on
	// 2026-09-18, 79 of 450 results (18%) carried a title from a real deep page
	// beside a URL that was only the site root, on 33 of the 45 queries. The
	// title is not stale — fetching the deep page that `fast` returns for the
	// identical title shows the live <title> is the same string — so the URL is
	// the field that is wrong. `fast` on the same 45 queries did it once in 450.
	//
	// That matters more here than it would elsewhere: a search result's URL is
	// what webfetch is handed next, so a wrong one sends the model to a
	// homepage where it finds plausible text and never learns it missed. The
	// defect is silent in exactly the way this harness cannot detect.
	//
	// Not configurable, because the choice is not a preference: `auto` is
	// measurably broken for this use and the user has no way to know. Reported
	// upstream; revisit when they answer. Note also that the accepted values
	// are wider than the reference documents — a rejected value comes back
	// naming neural, keyword, auto, hybrid, fast, blue, deep-reasoning,
	// deep-lite, magic, deep and instant — so a future change here has more to
	// choose from than the docs suggest.
	exaSearchType = "fast"
)

// exaResponse is the JSON envelope, decoding only what is used.
//
// `error` is a *string* here, unlike the object other services send, and it
// arrives beside a `tag` naming the condition and a `requestId` that is the
// only handle a user has when asking Exa about a failed query. Read off live
// responses rather than the reference, which documents the success shape only.
type exaResponse struct {
	//nolint:tagliatelle // Exa's own field names; the wire format is not ours to style.
	RequestID string `json:"requestId"`
	Error     string `json:"error"`
	Tag       string `json:"tag"`
	Results   []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		// The page's own extracted text, from contents.text. Absent when the
		// crawl found nothing, which is not a failure of the search.
		Text string `json:"text"`
		//nolint:tagliatelle // Exa's own field name, as above.
		PublishedDate string `json:"publishedDate"`
	} `json:"results"`
}

// NewExa returns a Searcher backed by the Exa search API. apiKey is required:
// unlike AnySearch there is no anonymous tier, and config refuses a search()
// naming this backend without one, so an empty key here means a bug rather than
// a user choice.
func NewExa(baseURL, apiKey string, transport http.RoundTripper, userAgent string) Searcher {
	if userAgent == "" {
		userAgent = scrapeUserAgentDefault
	}
	client := &http.Client{Transport: transport, Timeout: exaTimeout}
	return func(ctx context.Context, query string) (SearchResults, error) {
		body, err := json.Marshal(map[string]any{
			"query":      query,
			"numResults": searchMaxResults,
			"type":       exaSearchType,
			// Asking for exactly what is kept. The text is clipped to
			// searchMaxSnippet below either way, and it is the *opening* of the
			// page, so a larger window would buy nothing a reader sees.
			"contents": map[string]any{
				"text": map[string]any{"maxCharacters": searchMaxSnippet},
			},
		})
		if err != nil {
			return SearchResults{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/search",
			bytes.NewReader(body))
		if err != nil {
			return SearchResults{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		// x-api-key rather than Authorization: the endpoint accepts both, and
		// this is the one the reference leads with. Spelled canonically because
		// Set canonicalizes anyway, so the wire is X-Api-Key either way.
		req.Header.Set("X-Api-Key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return SearchResults{}, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, searchMaxBytes))
		if err != nil {
			return SearchResults{}, err
		}

		var out exaResponse
		if jsonErr := json.Unmarshal(raw, &out); jsonErr != nil {
			// The status first, for the reason the AnySearch client gives: a
			// proxy answering instead of the service is likelier than the
			// service having changed its format.
			if resp.StatusCode != http.StatusOK {
				return SearchResults{}, fmt.Errorf("HTTP %d from the service", resp.StatusCode)
			}
			return SearchResults{}, fmt.Errorf("the service did not return valid JSON: %w", jsonErr)
		}
		if err := exaError(resp.StatusCode, out); err != nil {
			return SearchResults{}, err
		}
		return exaToResults(query, out), nil
	}
}

// exaError turns a failure into something a user can act on.
//
// The 402 is the one worth naming. With no key at all the endpoint does not
// answer 401: it returns an x402 payment challenge — a JSON block quoting a
// price in a stablecoin, a chain id and a wallet address — which is a baffling
// thing to meet after forgetting api_key=. Config refuses a keyless exa search,
// so reaching this means the key went missing some other way, and saying so is
// more use than relaying the challenge.
func exaError(status int, out exaResponse) error {
	if status == http.StatusOK && out.Error == "" {
		return nil
	}
	msg := strings.TrimSpace(out.Error)
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", status)
	}
	switch status {
	case http.StatusPaymentRequired:
		return errors.New("the service answered with a payment challenge, which is what Exa " +
			"sends when no API key reached it. Check api_key= on search()")
	case http.StatusUnauthorized:
		return fmt.Errorf("the service rejected the API key (%s). Check api_key= on search()", msg)
	case http.StatusTooManyRequests:
		return errors.New("the service is rate-limiting this key (HTTP 429)")
	}
	if out.RequestID != "" {
		return fmt.Errorf("%s (request %s)", msg, out.RequestID)
	}
	return errors.New(msg)
}

// exaToResults reduces a decoded response. Exa reports no per-engine health —
// it is one index rather than a federation — so Unresponsive stays empty, the
// same as AnySearch and for the same reason: the degraded-search note is
// SearXNG's, and inventing one here would claim knowledge the service does not
// give.
func exaToResults(query string, out exaResponse) SearchResults {
	res := SearchResults{Query: query}
	for _, r := range out.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		if len(res.Results) == searchMaxResults {
			break
		}
		res.Results = append(res.Results, SearchResult{
			Title:   clipRunes(strings.TrimSpace(r.Title), searchMaxSnippet),
			URL:     strings.TrimSpace(r.URL),
			Content: clipRunes(strings.TrimSpace(r.Text), searchMaxSnippet),
			// Exa dates results where it knows one, which SearXNG also does and
			// AnySearch does not. It is an ISO-8601 instant rather than a day,
			// and passed through unparsed: the renderer prints it as given, and
			// a date Strument reformatted would be a date Strument could get
			// wrong for no gain.
			Published: clipRunes(strings.TrimSpace(r.PublishedDate), 64),
		})
	}
	return res
}

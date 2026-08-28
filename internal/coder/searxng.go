package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Searching the web, through the user's own SearXNG instance and nothing else.
//
// The scope is deliberate. SearXNG is self-hosted, so the instance belongs to
// the user, inherits the engines and policy they already chose, needs no API
// key, and puts no third party in a position Strument would have to speak for.
// A hosted search API would need all four decided on the user's behalf.
//
// It also means the instance has to have opted in. SearXNG ships
// `formats: [html]`, so JSON is off until an admin adds it, and eleven public
// instances were tried while designing this — none served JSON. Every failure
// below was seen for real, and each looks like something else, which is why
// they are translated rather than passed through.

const (
	searxTimeout = 30 * time.Second
	// Twenty per page is what an instance returns; ten is what a model can read
	// without the tail crowding out the answer. A query that needs the eleventh
	// result needs a better query.
	searxMaxResults = 10
	searxMaxBytes   = 4 * 1024 * 1024
	// A snippet is text from whatever page ranked, so its length is not the
	// instance's decision and not ours — a live instance returned about ninety
	// characters, and nothing stops a page returning a megabyte. Unbounded, one
	// result could push the other nine and the note about unresponsive engines
	// past the tool-result cap, which trims from the tail. Bounding each
	// snippet keeps the whole result small enough that nothing is ever cut.
	searxMaxSnippet = 400
)

// SearchResults is one answered query, already reduced to the fields worth
// carrying. A real result arrives with twenty-three of them — positions,
// parsed_url, open_group, thumbnail, score — and all but three are noise to a
// reader.
type SearchResults struct {
	Query   string
	Results []SearchResult
	Answers []string
	// Unresponsive pairs an engine with why it did not answer. It is the whole
	// reason this type is not just []SearchResult: on a real instance, a
	// perfectly good query came back with brave rate-limited, duckduckgo
	// showing a CAPTCHA and startpage timing out — and a query with no results
	// came back the same way. Without this, "the web has nothing" and "three
	// engines were broken" are the same answer, and a model will report the
	// first when it is looking at the second.
	Unresponsive []UnresponsiveEngine
}

// SearchResult is one hit.
type SearchResult struct {
	Title, URL, Content, Published string
}

// UnresponsiveEngine is one engine that did not answer, and why.
type UnresponsiveEngine struct{ Engine, Reason string }

// Searcher runs one query; injectable for tests.
type Searcher func(ctx context.Context, query string) (SearchResults, error)

// searxResponse mirrors SearXNG's JSON, decoding only what is used. Read off
// webutils.get_json_response rather than guessed: there is no
// number_of_results in current master, and unresponsive_engines is a list of
// two-element *arrays* rather than objects — both easy to assume wrongly and
// both confirmed against a live instance.
type searxResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
		//nolint:tagliatelle // SearXNG's own field name; the wire format is not ours to style.
		PublishedDate string `json:"publishedDate"`
	} `json:"results"`
	Answers             []json.RawMessage `json:"answers"`
	UnresponsiveEngines [][]string        `json:"unresponsive_engines"`
}

// NewSearxNG returns a Searcher backed by a SearXNG instance.
func NewSearxNG(baseURL string, transport http.RoundTripper, userAgent string) Searcher {
	if userAgent == "" {
		userAgent = scrapeUserAgentDefault
	}
	client := &http.Client{Transport: transport, Timeout: searxTimeout}
	return func(ctx context.Context, query string) (SearchResults, error) {
		form := url.Values{"q": {query}, "format": {"json"}}
		// POST, so a long query never meets a URL length limit in a reverse
		// proxy, and so the query stays out of the instance's access log the
		// way a GET's query string would not. SearXNG documents both.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/search",
			strings.NewReader(form.Encode()))
		if err != nil {
			return SearchResults{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return SearchResults{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, searxMaxBytes))
		if err != nil {
			return SearchResults{}, err
		}
		if err := searxHTTPError(resp, body); err != nil {
			return SearchResults{}, err
		}

		var raw searxResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			return SearchResults{}, fmt.Errorf("the instance did not return valid JSON: %w", err)
		}
		return searxToResults(raw), nil
	}
}

// searxHTTPError turns the three failures a real instance produces into
// something a reader can act on. Each was observed, and each is opaque raw.
func searxHTTPError(resp *http.Response, body []byte) error {
	ctype := resp.Header.Get("Content-Type")
	switch {
	case resp.StatusCode == http.StatusForbidden:
		// SearXNG's shipped default is `formats: [html]`, and requesting a
		// format that is not enabled is a 403. This is the first thing every
		// new instance does, so the message is the fix rather than the symptom.
		return errors.New("the instance refused a JSON search (HTTP 403). Add json under " +
			"search.formats in its settings.yml:\n  search:\n    formats:\n      - html\n      - json")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("the instance is rate-limiting this client (HTTP 429). Its limiter " +
			"plugin usually does this to non-browser clients; a self-hosted instance can turn it off")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("HTTP %d from the instance", resp.StatusCode)
	case !strings.Contains(ctype, "json"):
		// The one that looks like success. Two public instances answered 200
		// with "Verifying your browser…", which a status-only check accepts and
		// the decoder then reports as `invalid character '<'`.
		return fmt.Errorf("the instance answered with %s rather than JSON, which usually means a "+
			"bot check or a login page sitting in front of it", contentTypeName(ctype))
	}
	if len(body) == 0 {
		return errors.New("the instance returned an empty response")
	}
	return nil
}

func contentTypeName(ctype string) string {
	if t, _, ok := strings.Cut(ctype, ";"); ok {
		ctype = t
	}
	if ctype = strings.TrimSpace(ctype); ctype == "" {
		return "no content type"
	}
	return ctype
}

// searxToResults reduces a decoded response, dropping results with no URL —
// which a reader cannot act on — and capping the rest.
func searxToResults(raw searxResponse) SearchResults {
	out := SearchResults{Query: raw.Query}
	for _, r := range raw.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		if len(out.Results) == searxMaxResults {
			break
		}
		out.Results = append(out.Results, SearchResult{
			Title:     clipRunes(strings.TrimSpace(r.Title), searxMaxSnippet),
			URL:       strings.TrimSpace(r.URL),
			Content:   clipRunes(strings.TrimSpace(r.Content), searxMaxSnippet),
			Published: clipRunes(strings.TrimSpace(r.PublishedDate), 64),
		})
	}
	// An answer is a string in some versions and an object with an "answer"
	// field in others, so it is decoded late and leniently: an instant answer
	// is worth having and is never worth failing a search over.
	for _, a := range raw.Answers {
		if s := answerText(a); s != "" {
			out.Answers = append(out.Answers, s)
		}
	}
	for _, pair := range raw.UnresponsiveEngines {
		switch len(pair) {
		case 0:
			continue
		case 1:
			out.Unresponsive = append(out.Unresponsive, UnresponsiveEngine{Engine: pair[0]})
		default:
			out.Unresponsive = append(out.Unresponsive, UnresponsiveEngine{Engine: pair[0], Reason: pair[1]})
		}
	}
	return out
}

func answerText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Answer string `json:"answer"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Answer)
	}
	return ""
}

// clipRunes shortens to at most n runes, cutting on a rune boundary so a
// snippet in any script survives as text rather than as a broken tail byte.
func clipRunes(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

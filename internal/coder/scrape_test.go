package coder

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type scrapeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f scrapeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestNewSimpleScraperUsesTransport: the scraper fetches over the injected
// transport (this is how the global proxy reaches URL scraping) and still
// reduces HTML to text.
func TestNewSimpleScraperUsesTransport(t *testing.T) {
	used := false
	rt := scrapeRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		used = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html><body>Hello <b>world</b></body></html>")),
		}, nil
	})

	out, err := NewSimpleScraper(rt)(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("scraper did not dial through the injected transport")
	}
	if !strings.Contains(out, "Hello world") || strings.Contains(out, "<b>") {
		t.Errorf("HTML not reduced to text: %q", out)
	}
}

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

func htmlResp(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestScraperHTMLToMarkdown: HTML becomes markdown over the injected transport
// (how the proxy reaches scraping), media is stripped, and the request
// identifies itself with a User-Agent and Accept.
func TestScraperHTMLToMarkdown(t *testing.T) {
	var got *http.Request
	rt := scrapeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return htmlResp(`<html><body><h1>Title</h1><p>Hello <b>world</b> ` +
			`<a href="https://ex.com">link</a>.</p>` +
			`<img src="data:image/png;base64,AAAA"><svg></svg></body></html>`), nil
	})

	out, err := NewSimpleScraper(rt, "Strument/9.9.9")(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no request captured")
	}
	if ua := got.Header.Get("User-Agent"); ua != "Strument/9.9.9" {
		t.Errorf("User-Agent = %q, want Strument/9.9.9", ua)
	}
	if got.Header.Get("Accept") == "" {
		t.Error("Accept header not sent")
	}
	// Markdown, not raw tags; heading + bold + link survive.
	for _, want := range []string{"# Title", "**world**", "[link](https://ex.com)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Raw tags gone; media (image data URI, svg) stripped.
	for _, bad := range []string{"<b>", "<h1", "data:image", "<svg"} {
		if strings.Contains(out, bad) {
			t.Errorf("output should not contain %q:\n%s", bad, out)
		}
	}
}

// TestScraperNonHTMLVerbatim: a non-HTML content type is returned as text.
func TestScraperNonHTMLVerbatim(t *testing.T) {
	rt := scrapeRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("plain <b>not-html</b> text")),
		}, nil
	})
	out, err := NewSimpleScraper(rt, "")(context.Background(), "https://example.com/raw.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plain <b>not-html</b> text") {
		t.Errorf("non-HTML should be verbatim: %q", out)
	}
}

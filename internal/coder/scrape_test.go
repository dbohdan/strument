package coder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
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
// (how the proxy reaches scraping); media and chrome landmarks (nav/footer) are
// stripped, empty icon links are dropped, and the request identifies itself with
// a User-Agent and Accept.
func TestScraperHTMLToMarkdown(t *testing.T) {
	var got *http.Request
	rt := scrapeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return htmlResp(`<html><body>` +
			`<nav><a href="https://nav.example">Home</a></nav>` +
			`<h1>Title</h1><p>Hello <b>world</b> ` +
			`<a href="https://ex.com">link</a>.</p>` +
			`<a href="https://logo.example"><img src="data:image/png;base64,AAAA"></a>` +
			`<svg></svg>` +
			`<footer>Copyright NavCorp</footer></body></html>`), nil
	})

	out, err := NewSimpleScraper(rt, "Strument/9.9.9")(context.Background(), "https://example.com/page", ScrapeOptions{})
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
	// Raw tags gone; media (image data URI, svg) stripped; nav/footer chrome and
	// the emptied icon link dropped.
	for _, bad := range []string{
		"<b>", "<h1", "data:image", "<svg",
		"nav.example", "Copyright NavCorp", "https://logo.example",
	} {
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
	out, err := NewSimpleScraper(rt, "")(context.Background(), "https://example.com/raw.txt", ScrapeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plain <b>not-html</b> text") {
		t.Errorf("non-HTML should be verbatim: %q", out)
	}
}

// TestBuildScrapeArgs: the URL is substituted for every %s, and appended when no
// element carries the placeholder.
func TestBuildScrapeArgs(t *testing.T) {
	cases := []struct {
		argv []string
		url  string
		want []string
	}{
		{[]string{"chromium", "--dump-dom", "%s"}, "https://x/y", []string{"chromium", "--dump-dom", "https://x/y"}},
		{[]string{"fetch", "--url=%s", "--referer=%s"}, "u", []string{"fetch", "--url=u", "--referer=u"}},
		{[]string{"monolith"}, "https://z", []string{"monolith", "https://z"}},
	}
	for _, c := range cases {
		if got := buildScrapeArgs(c.argv, c.url); !slices.Equal(got, c.want) {
			t.Errorf("buildScrapeArgs(%v, %q) = %v, want %v", c.argv, c.url, got, c.want)
		}
	}
}

// TestCommandScraper drives the command path against this test binary re-run as
// the scrape command (the os/exec helper-process idiom): no sockets, no browser.
func TestCommandScraper(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	helper := func(mode string) []string {
		return []string{os.Args[0], "-test.run=TestScrapeHelperProcess", "--", mode, "%s"}
	}

	// The helper needs its own gate variable, and the only way to get one to the
	// command is the way a real user would: set it in the environment and name it
	// in env_allow. That makes this the observation that the allowlist is really
	// what reaches cmd.Env — under the old contract the test handed over a
	// finished NAME=VALUE pair and never exercised the filter at all. Passing the
	// closure rather than a snapshot is the production shape: /env changes take
	// effect on the next fetch.
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	scrapeEnv := func() []string { return []string{"GO_WANT_HELPER_PROCESS"} }
	out, err := NewCommandScraper(helper("ok"), 10*time.Second, scrapeEnv)(context.Background(), "https://example.com/page", ScrapeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The URL reached the command, and its stdout HTML became markdown.
	if !strings.Contains(out, "example.com/page") {
		t.Errorf("URL not passed to command:\n%s", out)
	}
	if !strings.Contains(out, "# Rendered") {
		t.Errorf("output not markdown-converted:\n%s", out)
	}
	if strings.Contains(out, "<h1>") {
		t.Errorf("raw HTML leaked:\n%s", out)
	}

	// A non-zero exit surfaces as an error carrying the stderr tail.
	_, err = NewCommandScraper(helper("fail"), 10*time.Second, scrapeEnv)(context.Background(), "https://x", ScrapeOptions{})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error missing stderr tail: %v", err)
	}
}

// TestScrapeHelperProcess is not a real test: it is the child the command
// scraper runs in TestCommandScraper. Guarded by GO_WANT_HELPER_PROCESS, it
// echoes the URL it received inside a scrap of HTML, or exits non-zero on "fail".
func TestScrapeHelperProcess(_ *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	mode, url := "", ""
	if len(args) > 0 {
		mode = args[0]
	}
	if len(args) > 1 {
		url = args[1]
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "boom: could not launch browser")
		os.Exit(3)
	}
	fmt.Printf("<html><body><h1>Rendered</h1><p>URL was %s</p></body></html>\n", url)
	os.Exit(0)
}

// The scraper filters its own environment, so a caller cannot construct one
// that inherits Strument's. exec.Cmd reads a nil Env as "inherit everything",
// which is where OPENROUTER_API_KEY lives.
func TestCommandScraperFiltersEvenWithNoAllowlist(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("STRUMENT_SCRAPE_SECRET", "do-not-leak")

	// nil envAllow: the shape a future caller gets by passing nothing.
	out, err := NewCommandScraper(
		[]string{os.Args[0], "-test.run=TestScrapeEnvHelperProcess", "--", "%s"},
		10*time.Second, nil,
	)(context.Background(), "https://example.com/", ScrapeOptions{})
	// The helper cannot run without its gate variable, so the fetch fails —
	// which is the point: a filtered environment breaks the command loudly
	// rather than leaking quietly.
	if err == nil && strings.Contains(out, "do-not-leak") {
		t.Fatal("the scraper passed an unfiltered environment to the command")
	}

	// And with the secret named in env_allow it does arrive, so the check above
	// is about filtering rather than about the variable never being passable.
	out, err = NewCommandScraper(
		[]string{os.Args[0], "-test.run=TestScrapeEnvHelperProcess", "--", "%s"},
		10*time.Second,
		func() []string { return []string{"GO_WANT_HELPER_PROCESS", "STRUMENT_SCRAPE_SECRET"} },
	)(context.Background(), "https://example.com/", ScrapeOptions{})
	if err != nil {
		t.Fatalf("allowed variable did not reach the command: %v", err)
	}
	if !strings.Contains(out, "do-not-leak") {
		t.Errorf("STRUMENT_SCRAPE_SECRET was in env_allow but did not reach the command:\n%s", out)
	}
}

// TestScrapeEnvHelperProcess echoes the secret it was given, if any.
func TestScrapeEnvHelperProcess(_ *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Printf("<h1>%s</h1>", os.Getenv("STRUMENT_SCRAPE_SECRET"))
	os.Exit(0)
}

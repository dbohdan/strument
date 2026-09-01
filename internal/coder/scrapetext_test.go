// Outlines for the text formats an outline used to be useless on: the Go
// source behind a raw.githubusercontent URL, the Markdown README, and the
// ReST file that must not be mistaken for Markdown.

package coder

import (
	"fmt"
	"strings"
	"testing"
)

func TestClassifyByExtension(t *testing.T) {
	cases := []struct {
		url, body, want string
	}{
		{"https://raw.githubusercontent.com/mvdan/sh/v3.13.0/interp/runner.go", "package interp\n", "code"},
		// Query and fragment must not ride on the extension: a GitHub line
		// anchor on a raw URL is a natural fetch after seeing one.
		{"https://example.com/interp.go?raw=1", "package interp\n", "code"},
		{"https://example.com/interp.go#L10", "package interp\n", "code"},
		{"https://example.com/README.md?plain=1", "# Title\n", "markdown"},
		{"https://example.com/README.md", "# Title\n", "markdown"},
		{"https://example.com/api.rst", "Title\n=====\n\nSection\n-------\n", "text"},
		{"https://example.com/manual.org", "* Heading one\n* Heading two\n", "text"},
		{"https://example.com/notes.txt", "# not a heading, just a hash\n#another\n", "text"},
		{"https://example.com/page", "<html><body><p>hi</p></body></html>", "html"},
		// An extensionless body that reads as Markdown: three ATX headings.
		{"https://example.com/README", "# Title\n\n## Install\n\n## Use\n\nbody text\n", "markdown"},
		// An extensionless body that reads as ReST: over/underline pairs and a
		// directive. The sniff must not call this Markdown.
		{"https://example.com/manual", "Title\n=====\n\nSection\n-------\n\n.. code-block:: python\n\n   x = 1\n", "text"},
	}
	for _, c := range cases {
		got := classifyBody(c.url, c.body).String()
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.url, got, c.want)
		}
	}
}

func (k contentKind) String() string {
	switch k {
	case kindHTML:
		return "html"
	case kindMarkdown:
		return "markdown"
	case kindCode:
		return "code"
	}
	return "text"
}

func TestGoSourceOutlineListsDefinitionsWithLines(t *testing.T) {
	src := `package interp

import "os"

// Run drives the interpreter.
func (i *Interpreter) Run(ctx context.Context) error {
	return nil
}

// Seconds is a timeout.
const timeout = 30

type Runner struct {
	done bool
}

func NewRunner() *Runner {
	return &Runner{}
}
`
	out := defOutlineOf("https://raw.githubusercontent.com/x/y/v1/interp.go", src, 0)
	for _, want := range []string{
		"method Run", "lines 6-8",
		"constant timeout", "lines 11-11",
		"class Runner", "lines 13-15",
		"function NewRunner", "lines 17-19",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outline missing %q:\n%s", want, out)
		}
	}
	// The nav line names the fetch that uses the numbers, and the range form
	// is the one the range parameter parses.
	if !strings.Contains(out, "range") {
		t.Errorf("the outline does not say how to fetch a range:\n%s", out)
	}
}

func TestMarkdownOutlineTracksFences(t *testing.T) {
	body := "# Title\n\nSome prose.\n\n```sh\n# this is a comment, not a heading\nls\n```\n\n## Install\n\npip install\n"
	out := markdownOutline(body)
	if strings.Contains(out, "this is a comment") {
		t.Errorf("a fenced shell comment became a heading:\n%s", out)
	}
	if !strings.Contains(out, "Install") || !strings.Contains(out, "(line 10)") {
		t.Errorf("the real heading is missing or mislocated:\n%s", out)
	}
}

func TestPlainTextOutlineOffersRanges(t *testing.T) {
	out := plainTextOutline(strings.Repeat("line\n", 499)+"last", 0)
	if !strings.Contains(out, "500 lines") {
		t.Errorf("the line count is wrong:\n%s", out)
	}
	if !strings.Contains(out, "fetch a range") {
		t.Errorf("the range escape is not named:\n%s", out)
	}
}

func TestRangeFetchSlicesLines(t *testing.T) {
	body := "one\ntwo\nthree\nfour\nfive\n"
	got := sliceLines(body, 2, 4)
	if got != "two\nthree\nfour" {
		t.Errorf("sliceLines(2,4) = %q", got)
	}
	// Clamped past the end rather than refused: the file's length is not
	// known until it is in hand.
	if got := sliceLines(body, 4, 99); got != "four\nfive" {
		t.Errorf("a range past the end did not clamp: %q", got)
	}
}

func TestParseLineRangeForms(t *testing.T) {
	ok := []struct {
		in     string
		lo, hi int
	}{
		{"80-120", 80, 120},
		{"80", 80, 80},
		{" 412 - 470 ", 412, 470},
	}
	for _, c := range ok {
		lo, hi, err := parseLineRange(c.in)
		if err != nil || lo != c.lo || hi != c.hi {
			t.Errorf("parseLineRange(%q) = %d,%d,%v", c.in, lo, hi, err)
		}
	}
	for _, bad := range []string{"0-5", "120-80", "a-b", "80-"} {
		if lo, _, err := parseLineRange(bad); err == nil {
			t.Errorf("parseLineRange(%q) accepted as %d", bad, lo)
		}
	}
	// The empty string is "no range", not an error.
	if lo, _, err := parseLineRange(""); err != nil || lo != 0 {
		t.Error("an absent range must parse as none")
	}
}

// P1: an oversized text page that arrives without outline:true must still get
// a kind-aware map — the old path ran the HTML heading scanner over it and
// answered "This page has no headings", with navigation advice (URL anchors)
// that a text page cannot act on.
func TestTruncatedCodePageCarriesADefOutline(t *testing.T) {
	var b strings.Builder
	b.WriteString("package interp\n\n")
	// Enough bulk to cross the 4x outline-switch threshold, but few enough
	// definitions for the outline to fit its own budget: wide functions, not
	// many of them. This walks the map-only path — the one a model is most
	// likely to meet without outline:true.
	for i := range 40 {
		fmt.Fprintf(&b, "func handler%d(w *Writer) {\n", i)
		b.WriteString(strings.Repeat("\twork(step)\n", 400))
		fmt.Fprintf(&b, "\t_ = w\n}\n\n")
	}
	wrapped := wrapContent("https://raw.githubusercontent.com/x/y/v1/interp.go", b.String())
	kind := classifyBody("https://raw.githubusercontent.com/x/y/v1/interp.go", b.String())
	got := truncateFetch(wrapped, "https://raw.githubusercontent.com/x/y/v1/interp.go", kind)

	if strings.Contains(got, "no headings") {
		t.Errorf("the text page got the HTML scanner's no-headings answer:\n%s", got[:300])
	}
	// The line numbers must be the file's, not the wrapped text's: the
	// framing line added two lines, and a range computed over the wrapped
	// text would fetch the wrong lines.
	if !strings.Contains(got, "lines 1-1") && !strings.Contains(got, "lines 5-7") {
		t.Errorf("the def outline's line numbers are not the file's:\n%s", got[len(got)-600:])
	}
	if !strings.Contains(got, "function handler0") || !strings.Contains(got, "function handler39") {
		t.Errorf("the map does not cover the whole file:\n%s", got[len(got)-600:])
	}
}

// P2: an outline computed over a range slice must report the file's line
// numbers, not the slice's — "lines 1-30" of a slice that starts at file line
// 100 would send the next fetch to the wrong place.
func TestOutlineOverARangeSliceKeepsFileLineNumbers(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\n")
	for i := range 120 { // lines 3..122 are filler functions
		fmt.Fprintf(&b, "func fill%d() {}\n\n", i)
	}
	body := b.String()
	sliced := sliceLines(body, 100, 122)
	out := defOutlineOf("https://example.com/p.go", sliced, 99)
	if !strings.Contains(out, "lines 101-101") {
		t.Errorf("an outline over a slice did not report file lines:\n%s", out)
	}

	// Through the full render path, with the range in the options.
	text, _ := renderFetched("https://example.com/p.go", body,
		ScrapeOptions{Outline: true, Range: "100-122"})
	if !strings.Contains(text, "lines 101-101") {
		t.Errorf("the rendered outline lost the offset:\n%s", text)
	}
}

// A range on an HTML page is said rather than silently misapplied: lines of
// raw markup are not a thing the model has seen, and the right move there is
// a URL fragment.
func TestRangeOnHTMLPageIsRefusedWithDirections(t *testing.T) {
	page := "<html><body><h1 id=\"a\">T</h1><p>body</p></body></html>"
	text, _ := renderFetched("https://example.com/p", page, ScrapeOptions{Range: "1-5"})
	if !strings.Contains(text, "range applies to plain-text pages") {
		t.Errorf("the range on HTML was applied or dropped silently:\n%s", text)
	}
	if !strings.Contains(text, "body") {
		t.Errorf("the page content did not come along:\n%s", text)
	}
}

// A shebang first line votes "code, not Markdown" on its own: a shell script's
// `# comment` lines match the ATX pattern, and three of them would otherwise
// clear the Markdown bar and hand the model a heading map of a script.
func TestShebangBeatsTheMarkdownSniff(t *testing.T) {
	script := "#!/usr/bin/env bash\n" +
		"# Set up the build directory.\n" +
		"# Then run the tests.\n" +
		"# Finally, package the result.\n" +
		"mkdir -p build && go test ./...\n"
	if got := classifyBody("https://example.com/setup", script); got != kindText {
		t.Errorf("a shebang script classified as %v; the sniff must not read its comments as headings", got)
	}

	// The same comments without the shebang stay ambiguous — the bar holds at
	// three headings either way; the shebang is the extra evidence, not the
	// only one.
	noShebang := strings.TrimPrefix(script, "#!/usr/bin/env bash\n")
	if got := classifyBody("https://example.com/setup", noShebang); got != kindMarkdown {
		t.Errorf("the same comments without a shebang classified as %v", got)
	}
}

// Outlines for the text formats an outline used to be useless on: the Go
// source behind a raw.githubusercontent URL, the Markdown README, and the
// ReST file that must not be mistaken for Markdown.

package coder

import (
	"strings"
	"testing"
)

func TestClassifyByExtension(t *testing.T) {
	cases := []struct {
		url, body, want string
	}{
		{"https://raw.githubusercontent.com/mvdan/sh/v3.13.0/interp/runner.go", "package interp\n", "code"},
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
	out := defOutlineOf("https://raw.githubusercontent.com/x/y/v1/interp.go", src)
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
	out := plainTextOutline(strings.Repeat("line\n", 499) + "last")
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

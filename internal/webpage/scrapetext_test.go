// Outlines for the text formats an outline used to be useless on: the Go
// source behind a raw.githubusercontent URL, the Markdown README, and the
// ReST file that must not be mistaken for Markdown.

package webpage

import (
	"fmt"
	"slices"
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
		got := Classify(c.url, c.body).String()
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.url, got, c.want)
		}
	}
}

func (k Kind) String() string {
	switch k {
	case KindHTML:
		return "html"
	case KindMarkdown:
		return "markdown"
	case KindCode:
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
		lo, hi, err := ParseLineRange(c.in)
		if err != nil || lo != c.lo || hi != c.hi {
			t.Errorf("ParseLineRange(%q) = %d,%d,%v", c.in, lo, hi, err)
		}
	}
	for _, bad := range []string{"0-5", "120-80", "a-b", "80-"} {
		if lo, _, err := ParseLineRange(bad); err == nil {
			t.Errorf("ParseLineRange(%q) accepted as %d", bad, lo)
		}
	}
	// The empty string is "no range", not an error.
	if lo, _, err := ParseLineRange(""); err != nil || lo != 0 {
		t.Error("an absent range must parse as none")
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
		Options{Outline: true, Range: "100-122"})
	if !strings.Contains(text, "lines 101-101") {
		t.Errorf("the rendered outline lost the offset:\n%s", text)
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
	if got := Classify("https://example.com/setup", script); got != KindText {
		t.Errorf("a shebang script classified as %v; the sniff must not read its comments as headings", got)
	}

	// The same comments without the shebang stay ambiguous — the bar holds at
	// three headings either way; the shebang is the extra evidence, not the
	// only one.
	noShebang := strings.TrimPrefix(script, "#!/usr/bin/env bash\n")
	if got := Classify("https://example.com/setup", noShebang); got != KindMarkdown {
		t.Errorf("the same comments without a shebang classified as %v", got)
	}
}

// A range on an HTML page counts lines of the text it converts to, which is
// what an outline of that text numbers (the coder's
// TestOversizedHTMLOutlineRangeFetchesItsSection takes the whole trip).
func TestARangeOnHTMLSlicesItsConvertedText(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body><h1 id=\"top\">Guide</h1>")
	for i := range 40 {
		fmt.Fprintf(&b, "<h2 id=\"s%d\">Section %d</h2><p>marker-%d</p>", i, i, i)
	}
	b.WriteString("</body></html>")
	page := b.String()
	const u = "https://example.com/guide"

	md := htmlToMarkdown(page, u, Options{})
	lo := slices.IndexFunc(strings.Split(md, "\n"), func(l string) bool { return strings.HasPrefix(l, "## Section 25 ") }) + 1
	if lo == 0 {
		t.Fatalf("no section 25 in the conversion:\n%s", md)
	}
	part, _ := renderFetched(u, page, Options{Range: fmt.Sprintf("%d-%d", lo, lo+3)})
	if !strings.HasPrefix(part, "## Section 25 ") || !strings.Contains(part, "marker-25") {
		t.Errorf("the range did not fetch its section:\n%s", part)
	}
	// An outline over a range of the converted text keeps the page's numbers.
	out, _ := renderFetched(u, page, Options{Outline: true, Range: fmt.Sprintf("%d-%d", lo, lo+20)})
	if !strings.Contains(out, fmt.Sprintf("(line %d)", lo)) {
		t.Errorf("an outline over a range lost the page's line numbers (want line %d):\n%s", lo, out)
	}
}

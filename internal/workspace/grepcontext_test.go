// Lines around a match: merging, marking, and the cap.

package workspace

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func contextRepo(t *testing.T, body string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(dir)
}

// render is the shape the tool layer prints, so a test can assert on what the
// model would actually read.
func render(res GrepResult) string {
	var b strings.Builder
	for _, f := range res.Files {
		for _, l := range f.Lines {
			if l.GapBefore {
				b.WriteString("--\n")
			}
			sep := ":"
			if !l.Match {
				sep = "-"
			}
			b.WriteString(sep + strconv.Itoa(l.Number) + sep + " " + l.Text + "\n")
		}
	}
	return b.String()
}

func TestGrepContextReturnsSurroundingLines(t *testing.T) {
	w := contextRepo(t, "one\ntwo\nTARGET\nfour\nfive\n")
	res, err := w.Grep(GrepQuery{Pattern: "TARGET", Mode: GrepContent, ContextLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := render(res)
	want := "-2- two\n:3: TARGET\n-4- four\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Every matching line keeps its marker, even one that was already on screen as
// context for an earlier match. Marking during the walk got this wrong: the
// line was emitted as context and never re-marked, so a hit vanished from a
// listing that had counted it. Found by running the tool, not by reading it.
func TestGrepContextMarksEveryMatchEvenInsideAnEarlierWindow(t *testing.T) {
	w := contextRepo(t, "one\nTARGET a\nTARGET b\nfour\n")
	res, err := w.Grep(GrepQuery{Pattern: "TARGET", Mode: GrepContent, ContextLines: 2})
	if err != nil {
		t.Fatal(err)
	}
	got := render(res)
	if strings.Contains(got, "-3- TARGET b") {
		t.Errorf("a matching line was rendered as context:\n%s", got)
	}
	if !strings.Contains(got, ":2: TARGET a") || !strings.Contains(got, ":3: TARGET b") {
		t.Errorf("not every match is marked:\n%s", got)
	}
	if strings.Count(got, "TARGET b") != 1 {
		t.Errorf("a line was emitted twice by overlapping windows:\n%s", got)
	}
	if res.Total != 2 {
		t.Errorf("Total = %d, want 2", res.Total)
	}
}

func TestGrepContextSeparatesDistantBlocksAndNotTheFirst(t *testing.T) {
	body := "TARGET one\n" + strings.Repeat("filler\n", 20) + "TARGET two\n"
	w := contextRepo(t, body)
	res, err := w.Grep(GrepQuery{Pattern: "TARGET", Mode: GrepContent, ContextLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := render(res)
	if strings.HasPrefix(got, "--") {
		t.Errorf("a gap marker before the first block:\n%s", got)
	}
	if strings.Count(got, "--\n") != 1 {
		t.Errorf("want exactly one gap marker between the two blocks:\n%s", got)
	}
}

// Context clamps at the file's edges rather than inventing lines.
func TestGrepContextClampsAtFileEdges(t *testing.T) {
	w := contextRepo(t, "TARGET\nsecond\n")
	res, err := w.Grep(GrepQuery{Pattern: "TARGET", Mode: GrepContent, ContextLines: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range res.Files[0].Lines {
		if l.Number < 1 || l.Number > 2 {
			t.Errorf("line %d is outside the file", l.Number)
		}
	}
}

// The cap counts what is emitted, not what matched. Counting matches would let
// five lines of context turn a hundred hits into eleven hundred lines.
func TestGrepContextCapCountsEmittedLines(t *testing.T) {
	var b strings.Builder
	for range 400 {
		b.WriteString("TARGET\nfiller\nfiller\n")
	}
	w := contextRepo(t, b.String())
	res, err := w.Grep(GrepQuery{Pattern: "TARGET", Mode: GrepContent, ContextLines: 3})
	if err != nil {
		t.Fatal(err)
	}
	n := len(res.Files[0].Lines)
	if n > w.Limits.matches() {
		t.Errorf("emitted %d lines against a cap of %d", n, w.Limits.matches())
	}
	if !res.Truncated.Results {
		t.Error("a capped search did not report itself truncated")
	}
}

// Zero context is what it always was, so nothing changes for a caller that
// does not ask.
func TestGrepWithoutContextIsUnchanged(t *testing.T) {
	w := contextRepo(t, "one\nTARGET\nthree\n")
	res, err := w.Grep(GrepQuery{Pattern: "TARGET", Mode: GrepContent})
	if err != nil {
		t.Fatal(err)
	}
	if got := render(res); got != ":2: TARGET\n" {
		t.Errorf("got %q", got)
	}
}

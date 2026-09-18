package coder

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// read_text promises "a file's text exactly as stored", and the promise has to
// hold byte for byte, because every use of it is a computation over those
// bytes.
//
// It did not, and the trial's pilot is what found it. splitLines drops the
// file's final newline and nothing recorded that it had, so rejoining produced
// a file one byte short — and, for a file ending in a blank line, one line
// short. A model asked how many blank lines a file had wrote a careful,
// correct program, stripped the trailing element after splitting exactly as one
// should, and answered 3 where the file has 4. Nothing in its transcript was
// wrong. The harness had eaten the line.
func TestReadTextRoundTripsTheFile(t *testing.T) {
	for _, body := range []string{
		"a\nb\n",     // the ordinary case: terminated
		"a\nb",       // no final newline
		"a\n\n",      // a blank final line — the case that produced the wrong count
		"a\n\n\n",    // two of them
		"\n",         // nothing but a blank line
		"one line\n", //
		"",           // empty file
	} {
		t.Run(fmt.Sprintf("%q", body), func(t *testing.T) {
			c, _ := observeEnv(t, map[string]string{"f.txt": body})
			c.CodeReadText = true
			// Byte count and newline count rather than repr: Monty quotes the
			// way Python does and Go's %q does not, and an assertion that
			// compares quoting styles fails on a correct round-trip — which is
			// what this test did on its first run, reporting a bug that was in
			// itself.
			got := c.runCode(context.Background(), codeCall{
				code: `t = read_text("f.txt")` + "\n" + `print(len(t), t.count("\n"))`})
			want := fmt.Sprintf("%d %d", len(body), strings.Count(body, "\n"))
			if got != want {
				t.Errorf("stored %q: read_text gave (length, newlines) = %s, want %s", body, got, want)
			}
		})
	}
}

// The property the round-trip exists for, stated as the question a model asks:
// counting blank lines through read_text must agree with counting them in the
// file. A test on repr alone would pass on a change that fixed the quoting and
// not the content.
func TestReadTextCountsBlankLinesCorrectly(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"f.txt": "a\n\nb\n\n"})
	c.CodeReadText = true
	got := c.runCode(context.Background(), codeCall{
		code: "t = read_text(\"f.txt\")\nlines = t.split(\"\\n\")\n" +
			"if lines and lines[-1] == \"\":\n    lines = lines[:-1]\n" +
			"print(sum(1 for l in lines if l == \"\"))"})
	if got != "2" {
		t.Errorf("blank lines through read_text = %s, want 2 — the file has two", got)
	}
}

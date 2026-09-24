package coder

import (
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
			// Byte count and newline count rather than a quoted rendering: an
			// assertion that compares quoting styles fails on a correct
			// round-trip — which is what this test did on its first run,
			// reporting a bug that was in itself. The fixtures are ASCII, so
			// JavaScript's UTF-16 length is the byte count.
			got := run(c, `const t = read_text("f.txt");`+"\n"+
				`console.log(t.length, t.split("\n").length - 1)`)
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
	got := run(c, "const lines = read_text(\"f.txt\").split(\"\\n\");\n"+
		"if (lines.length && lines[lines.length - 1] === \"\") lines.pop();\n"+
		"lines.filter(l => l === \"\").length")
	if got != "2" {
		t.Errorf("blank lines through read_text = %s, want 2 — the file has two", got)
	}
}

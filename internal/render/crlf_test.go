package render

import (
	"strings"
	"testing"
)

func renderMarkdown(md string, byChar bool) string {
	var b strings.Builder
	r := NewANSI(&b, false, Theme{}, 80)
	p := NewParser(r)
	if byChar {
		for _, c := range md {
			p.Write(string(c))
		}
	} else {
		p.Write(md)
	}
	p.End()
	return b.String()
}

// TestCRLFRendersLikeLF pins the fence-leak fix: some models stream CRLF line
// endings, and a "\r\n" fence close used to read as body text, leaking the ```
// (and every downstream fence) into the visible output. CRLF must now render
// byte-identically to LF — whole and char by char, the latter exercising a
// "\r\n" split across two Writes.
func TestCRLFRendersLikeLF(t *testing.T) {
	lfDocs := []string{
		"Here is code:\n```go\nfmt.Println(1)\n```\nThanks.\n",
		"```go\nx()\n```\n",
		"a\n```go\nA\n```\n```py\nB\n```\ndone\n",
		"# H\n\ntext\n\n- one\n- two\n",
		"> quote\n> more\n",
	}
	for _, lf := range lfDocs {
		crlf := strings.ReplaceAll(lf, "\n", "\r\n")
		for _, byChar := range []bool{false, true} {
			want := renderMarkdown(lf, byChar)
			if got := renderMarkdown(crlf, byChar); got != want {
				t.Errorf("CRLF != LF (byChar=%v)\n  lf=%q\n   got=%q\n  want=%q", byChar, lf, got, want)
			} else if strings.ContainsRune(got, '`') {
				t.Errorf("CRLF render leaked a backtick (byChar=%v): %q", byChar, got)
			}
		}
	}
}

package render

import (
	"fmt"
	"io"
	"strings"
)

// The delimiters around a run_code program. Reuses thinking's closing marker:
// a program, like a think, is a model-authored aside the harness brackets, and
// one close glyph for both keeps "‹/› ends an aside" a single rule a reader
// learns once. Tag-shaped and deliberately not tag-valid, for the reason
// thinking.go records: stripReasoning removes <tag>…</tag> from model output,
// so printing that shape would make the harness's voice indistinguishable from
// the model's.
const (
	CodeOpen  = "‹run_code›"
	CodeClose = ThinkingClose
)

// ToolBlock renders one tool's multi-line payload — a program's source — in
// the shape thinking uses for a reasoning block: a bracketed block past one
// line, a bare prefixed line for a single-line call.
//
//	‹run_code›
//	for name in tools:
//	    caps[name] = grep(pattern=name)
//	‹/›
//
// versus
//
//	‹run_code› ls()
//
// The body is preserved verbatim, not flattened: Python's indentation carries
// the program's structure, and oneLine (the flattening this replaces) turned a
// 20-line program into one terminal-wrapping line of run-together tokens — the
// failure a live session reported as borderline-unreadable.
//
// Everything here is model-influenced text next to harness-written delimiters,
// so it goes through Sanitize, which keeps newlines.
func ToolBlock(w io.Writer, title, body string) {
	body = Sanitize(strings.TrimSpace(body))
	if !strings.Contains(body, "\n") {
		fmt.Fprintf(w, "%s %s\n", title, body)
		return
	}
	fmt.Fprintf(w, "%s\n%s\n%s\n", title, body, CodeClose)
}

package repl

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/render"
)

// escapeSeq matches any escape sequence, not only the SGR ones the renderer
// writes: the point is to find the ones it did not write.
var escapeSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[\x40-\x7e]|\x1b\][^\x07]*\x07|\x1b.`)

// TestNoUntrustedEscapeReachesTheTerminal is the guarantee, and it is a test
// rather than a list because a list goes stale. Every public way text enters
// termOutput is driven with a hostile payload; the only escapes allowed out are
// the renderer's own SGR.
//
// A terminal does what an escape sequence says — retitle the window, clear the
// screen, move the cursor back over text already on screen. That last one is
// what matters in a harness whose review surface is what the user reads off the
// scrollback: an answer that can reposition the cursor can rewrite the diff the
// user is deciding on.
//
// The payload is an OSC because that is what actually got through before. A
// code reading suggested tool arguments were quoted and the answer stream was
// raw; on the wire quoteToolArg added quotes and nothing else, while the answer
// stream stripped "\r" and mangled CSI on the markdown parser's "[" — so the
// one sequence that survived both paths was the one nobody predicted.
func TestNoUntrustedEscapeReachesTheTerminal(t *testing.T) {
	const payload = "x\x1b]0;PWNED\x07y"

	for _, tc := range []struct {
		name  string
		drive func(*termOutput)
	}{
		{"answer text", func(o *termOutput) { o.StreamText(payload) }},
		{"reasoning", func(o *termOutput) { o.StreamReasoning(payload) }},
		{"prose held behind a tool call", func(o *termOutput) {
			o.StreamToolCall(0, "edit", editArgs("a.md", "alpha", "ALPHA"))
			o.StreamText(payload)
		}},
		{"a tool outcome line", func(o *termOutput) { o.Toolf("Read %s", payload) }},
		{"a warning", func(o *termOutput) { o.Warningf("Skipping %s", payload) }},
		{"an error", func(o *termOutput) { o.Errorf("Could not read %s", payload) }},
		{"the harness's own voice", func(o *termOutput) { o.Printf("Added %s", payload) }},
		// editArgs uses Go's %q, whose \x1b is not a JSON escape, so the ESC
		// would never survive decoding and the case would test nothing.
		{"an edit's diff body", func(o *termOutput) {
			o.StreamToolCall(0, "edit", jsonEdit("a.md", payload, "clean"))
		}},
		{"an edit's path", func(o *termOutput) {
			o.StreamToolCall(0, "edit", jsonEdit(payload, "alpha", "ALPHA"))
		}},
		{"a shell command's text", func(o *termOutput) {
			o.StreamToolCall(0, "bash", `{"command":`+quoteJSON(payload)+`,"purpose":"p"}`)
		}},
		{"command output", func(o *termOutput) { o.Printf("%s", payload) }},
	} {
		for _, color := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				var buf bytes.Buffer
				o := &termOutput{w: &buf, color: color, theme: render.DefaultTheme(), width: 60}
				tc.drive(o)
				o.FlushStream()

				got := buf.String()
				if strings.Contains(got, "PWNED") {
					t.Errorf("color=%v: the escape's payload reached the terminal:\n%q", color, got)
				}
				for _, esc := range escapeSeq.FindAllString(got, -1) {
					// The renderer's own styling is the one thing allowed
					// through: SGR sets colors and moves nothing.
					if !strings.HasSuffix(esc, "m") && esc != "\x1b[K" &&
						esc != "\x1b[?25l" && esc != "\x1b[?25h" {
						t.Errorf("color=%v: unexpected escape %q in:\n%q", color, esc, got)
					}
				}
			})
		}
	}
}

// jsonEdit builds an edit call's arguments with real JSON escapes.
func jsonEdit(path, old, replacement string) string {
	return `{"path":` + quoteJSON(path) + `,"old_string":` + quoteJSON(old) +
		`,"new_string":` + quoteJSON(replacement) + `}`
}

// quoteJSON is a minimal JSON string literal for the payloads above.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(s) {
		switch c := s[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c < 0x20 {
				b.WriteString(escapeHex(c))
				continue
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func escapeHex(c byte) string {
	const hex = "0123456789abcdef"
	return `\u00` + string(hex[c>>4]) + string(hex[c&0xf])
}

// Sanitize keeps the text a reader needs and drops only what a terminal would
// act on, so an ordinary answer must pass through unchanged and unallocated.
func TestSanitizeLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{
		"", "plain text", "two\nlines\n", "a\ttab", "unicode: … ‹thinking› é",
		"markdown **bold** and `code`",
	} {
		if got := render.Sanitize(s); got != s {
			t.Errorf("Sanitize(%q) = %q, want it unchanged", s, got)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"a\x1b]0;t\x07b", "ab"},
		{"a\x1b[2Jb", "ab"},
		{"a\x1b[31mb", "ab"},
		{"a\rb", "ab"},
		{"a\x00b", "ab"},
		{"a\x7fb", "ab"},
		{"a\x1bPq\x1b\\b", "ab"}, // DCS
		{"trailing \x1b", "trailing "},
		{"keep\nnewlines\tand tabs", "keep\nnewlines\tand tabs"},
	} {
		if got := render.Sanitize(tc.in); got != tc.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

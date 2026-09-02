package readline

import (
	"strings"
	"testing"
)

// newRedrawTestBuf builds a runeBuffer backed by a terminal with a fixed width
// and prompt, without the goroutines/fds newTerminal would start — redraw only
// reads GetWidthHeight and GetConfig.
func newRedrawTestBuf(width int, prompt string, buf []rune, idx int) *runeBuffer {
	tm := &terminal{}
	tm.cfg.Store(&Config{Prompt: prompt, Painter: defaultPainter, isInteractive: true})
	tm.dimensions.Store(&termDimensions{width: width, height: 30})
	return &runeBuffer{w: tm, buf: buf, idx: idx}
}

// TestRedrawIsNonDestructive locks in bestline's anti-flicker property: the
// repaint overwrites the prompt and buffer in place and only erases (\x1b[J)
// *after* the content — never a screen-clear before the redraw, which is what
// made the old clean()+print() path flicker on slow/remote terminals.
func TestRedrawIsNonDestructive(t *testing.T) {
	// 45 runes at width 40 wraps to two rows; cursor sits mid-line.
	line := []rune("the quick brown fox jumps over the lazy dogs!")
	rb := newRedrawTestBuf(40, "> ", line, 30)
	tWidth, _ := rb.w.GetWidthHeight()
	out := string(rb.redraw(rb.idxLine(tWidth), tWidth))

	jPos := strings.Index(out, "\x1b[J")
	if jPos < 0 {
		t.Fatalf("redraw must emit a trailing \\x1b[J to trim a longer previous render:\n%q", out)
	}
	// The whole buffer must be drawn before the erase.
	if end := strings.Index(out, "dogs!"); end < 0 || jPos < end {
		t.Errorf("\\x1b[J (at %d) must come after the buffer content (ends at %d) — the erase is a trailing trim, not a destructive pre-clear:\n%q", jPos, end, out)
	}
	// And nothing may clear the display before the prompt is repainted.
	if p := strings.Index(out, "> "); p < 0 || jPos < p {
		t.Errorf("redraw must not erase before repainting the prompt:\n%q", out)
	}
}

// TestRedrawSingleLine covers the common unwrapped case: reposition to the
// prompt column, draw prompt+buffer, trailing trim.
func TestRedrawSingleLine(t *testing.T) {
	rb := newRedrawTestBuf(80, "> ", []rune("hello world"), 11)
	out := string(rb.redraw(0, 80))

	for _, want := range []string{"\x1b[1G", "> ", "hello world", "\x1b[J"} {
		if !strings.Contains(out, want) {
			t.Errorf("single-line redraw missing %q:\n%q", want, out)
		}
	}
	if bp, jp := strings.Index(out, "hello world"), strings.Index(out, "\x1b[J"); jp < bp {
		t.Errorf("erase must follow the buffer text:\n%q", out)
	}
}

// TestRedrawWrapsRowsItself locks in the prompt_toolkit-style render: the
// frame is drawn with autowrap disabled and explicit row breaks, so a
// character landing in the last column can never leave the cursor on a
// phantom wrapped cell (the artifact that made a character flash as erased
// between frames).
func TestRedrawWrapsRowsItself(t *testing.T) {
	// 45 runes at width 40 must break into two explicit rows.
	line := []rune("the quick brown fox jumps over the lazy dogs!")
	rb := newRedrawTestBuf(40, "> ", line, len(line))
	out := string(rb.redraw(rb.idxLine(40), 40))

	if !strings.Contains(out, "\x1b[?7l") || !strings.Contains(out, "\x1b[?7h") {
		t.Errorf("redraw must disable and restore autowrap around the frame:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[?25l") || !strings.Contains(out, "\x1b[?25h") {
		t.Errorf("redraw must hide and restore the cursor around the frame:\n%q", out)
	}
	if strings.Count(out, "\r\n") != 1 {
		t.Errorf("a line longer than the width must emit exactly one explicit row break, got %d:\n%q", strings.Count(out, "\r\n"), out)
	}
	// Cursor lands after the last drawn row ("y dogs!"), placed explicitly.
	if !strings.Contains(out, "\x1b[8G") {
		t.Errorf("redraw must place the final cursor column explicitly:\n%q", out)
	}
}

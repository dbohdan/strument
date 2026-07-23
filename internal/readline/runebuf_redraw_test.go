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

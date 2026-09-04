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

// TestRedrawLeavesWrappingToTheTerminal is the inverse of what the
// prompt_toolkit-style render asserted, and the reason it was reverted: rows
// must be the terminal's own autowrap, not explicit breaks. Hard breaks make
// every row a separate logical line, which costs reflow on resize (the terminal
// can no longer re-wrap the prompt, and never re-joins rows when the pane
// widens) and breaks find-in-scrollback, since a search matches across a
// wrapped line but not across a hard one.
func TestRedrawLeavesWrappingToTheTerminal(t *testing.T) {
	// 45 runes at width 40: the terminal wraps it, the render does not.
	line := []rune("the quick brown fox jumps over the lazy dogs!")
	rb := newRedrawTestBuf(40, "> ", line, len(line))
	out := string(rb.redraw(rb.idxLine(40), 40))

	for _, esc := range []string{"\x1b[?7l", "\x1b[?7h"} {
		if strings.Contains(out, esc) {
			t.Errorf("redraw must leave autowrap alone (found %q) — the terminal owns the row rule:\n%q", esc, out)
		}
	}
	if strings.Contains(out, "\r\n") {
		t.Errorf("redraw must not place row breaks itself:\n%q", out)
	}
}

package readline

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/readline/internal/runes"
)

// termCursor replays the subset of escape sequences redraw emits and tracks
// where they leave the cursor, in rows relative to the row the stream started
// on. Autowrap is off for the duration of a frame (redraw sets \e[?7l), so a
// rune landing in the last column does not advance the row — which is the whole
// point of the checks below: only an explicit break moves the render down.
type termCursor struct {
	row, col int
	// row the first visible rune was drawn on, i.e. where the prompt landed.
	firstDrawRow int
	drew         bool
}

func (t *termCursor) apply(s string) {
	rs := []rune(s)
	for i := 0; i < len(rs); {
		switch {
		case rs[i] == '\033' && i+1 < len(rs) && rs[i+1] == '[':
			j := i + 2
			for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
				j++
			}
			if j >= len(rs) {
				return
			}
			n, _ := strconv.Atoi(string(rs[i+2 : j]))
			switch rs[j] {
			case 'A':
				t.row -= max(n, 1)
			case 'B':
				t.row += max(n, 1)
			case 'G':
				t.col = max(n, 1) - 1
			}
			i = j + 1
		case rs[i] == '\r':
			t.col = 0
			i++
		case rs[i] == '\n':
			t.row++
			i++
		default:
			if !t.drew {
				t.firstDrawRow, t.drew = t.row, true
			}
			t.col += runes.Width(rs[i])
			i++
		}
	}
}

// TestRedrawRowsMatchTheCursorModel pins the property the row-placed render was
// introduced for and did not have: the rows redraw actually draws are the rows
// getSplitByLine counts. They diverged for exactly one buffer length per width
// — the one that fills the last column — because SplitByLine appends a trailing
// empty row there (its "the next character starts a new line" marker) and
// writeContent emitted no matching break.
func TestRedrawRowsMatchTheCursorModel(t *testing.T) {
	for _, tWidth := range []int{20, 40, 101} {
		for _, prompt := range []string{"> ", "\x1b[32m> \x1b[0m", "much longer prompt: "} {
			for n := 0; n <= 3*tWidth; n++ {
				line := []rune(strings.Repeat("x", n))
				rb := newRedrawTestBuf(tWidth, prompt, line, len(line))
				out := string(rb.redraw(rb.idxLine(tWidth), tWidth))

				if got, want := strings.Count(out, "\r\n"), rb.LineCount()-1; got != want {
					t.Fatalf("width %d, prompt %q, %d runes: render drew %d row breaks, cursor model counts %d rows:\n%q",
						tWidth, prompt, n, got, want, out)
				}

				// And the cursor must land where lastRowColumn says it does.
				c := &termCursor{}
				c.apply(out)
				if got, want := c.col+1, rb.lastRowColumn(); got != want {
					t.Fatalf("width %d, prompt %q, %d runes: frame leaves the cursor in column %d, lastRowColumn says %d:\n%q",
						tWidth, prompt, n, got, want, out)
				}
			}
		}
	}
}

// TestTypingLeavesTheCursorWhereTheModelExpects walks the fast path — typing at
// the end of the buffer never reaches redraw, it goes through append — and
// checks after every keystroke that the row and column the terminal is left in
// are the ones the cursor model would compute. This is the path that actually
// crosses the right edge in normal use, and the one where the terminal's own
// autowrap used to park the cursor on a phantom cell a row above where the
// model believed it was.
func TestTypingLeavesTheCursorWhereTheModelExpects(t *testing.T) {
	for _, tWidth := range []int{20, 40, 101} {
		var out bytes.Buffer
		rb := newRedrawTestBuf(tWidth, "> ", nil, 0)
		rb.getConfig().Stdout = &out

		c := &termCursor{}
		rb.Print()
		c.apply(out.String())

		for n := 1; n <= 3*tWidth; n++ {
			out.Reset()
			rb.WriteRune('x')
			c.apply(out.String())

			if got, want := c.row, rb.LineCount()-1; got != want {
				t.Fatalf("width %d, %d runes typed: cursor is on row %d, model says row %d", tWidth, n, got, want)
			}
			if got, want := c.col+1, rb.lastRowColumn(); got != want {
				t.Fatalf("width %d, %d runes typed: cursor is in column %d, model says %d", tWidth, n, got, want)
			}
		}
	}
}

// TestRedrawKeepsThePromptAnchored is the user-visible half of the same defect:
// a redraw moves up by the row the *previous* frame left the cursor on, so if
// the render and the model disagree by a row the prompt gets repainted one row
// too high and eats the output line above it. Backspacing across the right edge
// was the way in.
func TestRedrawKeepsThePromptAnchored(t *testing.T) {
	const tWidth, prompt = 40, "> "

	for n := 1; n <= 2*tWidth; n++ {
		line := []rune(strings.Repeat("x", n))
		rb := newRedrawTestBuf(tWidth, prompt, line, len(line))

		first := &termCursor{}
		first.apply(string(rb.redraw(rb.idxLine(tWidth), tWidth)))

		// Exactly what refresh does for a Backspace: capture the cursor's row
		// within the drawn block *before* the mutation, then redraw.
		idxLine := rb.idxLine(tWidth)
		rb.buf, rb.idx = line[:n-1], n-1

		second := &termCursor{row: first.row, col: first.col}
		second.apply(string(rb.redraw(idxLine, tWidth)))

		if second.firstDrawRow != first.firstDrawRow {
			t.Fatalf("%d runes: backspace repainted the prompt on row %d, but it was drawn on row %d — the redraw overwrites the line above",
				n, second.firstDrawRow, first.firstDrawRow)
		}
	}
}

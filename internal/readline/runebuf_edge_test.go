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

// termScreen replays a frame onto a grid of cells. termCursor above tracks only
// where the cursor lands, which is blind to a whole class of defect: a frame can
// leave the cursor in exactly the right place having erased content on the way.
// Autowrap is off for a frame, so a rune written in the last column leaves the
// cursor *on* that column instead of past it, and \e[K / \e[J erase from the
// cursor inclusive — the combination that ate a character per full row.
type termScreen struct {
	rows     [][]rune
	row, col int
	width    int
}

func newTermScreen(width int) *termScreen {
	return &termScreen{width: width}
}

func (s *termScreen) at(row int) []rune {
	for len(s.rows) <= row {
		s.rows = append(s.rows, bytes.Runes(bytes.Repeat([]byte{' '}, s.width)))
	}
	return s.rows[row]
}

func (s *termScreen) eraseToEOL() {
	r := s.at(s.row)
	for c := s.col; c < s.width; c++ {
		r[c] = ' '
	}
}

func (s *termScreen) apply(str string) {
	rs := []rune(str)
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
			n, _ := strconv.Atoi(strings.TrimLeft(string(rs[i+2:j]), "?"))
			switch rs[j] {
			case 'A':
				s.row = max(s.row-max(n, 1), 0)
			case 'B':
				s.row += max(n, 1)
			case 'G':
				s.col = max(n, 1) - 1
			case 'K':
				s.eraseToEOL()
			case 'J':
				s.eraseToEOL()
				s.rows = s.rows[:min(s.row+1, len(s.rows))]
			}
			i = j + 1
		case rs[i] == '\r':
			s.col = 0
			i++
		case rs[i] == '\n':
			s.row++
			s.at(s.row)
			i++
		default:
			s.at(s.row)[min(s.col, s.width-1)] = rs[i]
			// Autowrap off: the cursor stops on the last column rather than
			// moving past it, which is why an erase there is destructive.
			s.col = min(s.col+runes.Width(rs[i]), s.width-1)
			i++
		}
	}
}

// text reads the grid back as one string, undoing the row breaks the render
// placed, so it can be compared against what was meant to be on screen.
func (s *termScreen) text() string {
	var b strings.Builder
	for _, r := range s.rows {
		b.WriteString(string(r))
	}
	return strings.TrimRight(b.String(), " ")
}

// TestRenderedRowsKeepEveryCharacter is the screen-level counterpart to
// TestRedrawRowsMatchTheCursorModel: every rune of prompt and buffer must
// actually be on the grid afterwards. It fails for every buffer that fills a
// row exactly, which on a narrow terminal is most of them.
func TestRenderedRowsKeepEveryCharacter(t *testing.T) {
	for _, tWidth := range []int{14, 18, 25, 40} {
		for _, prompt := range []string{"> ", "\x1b[32m> \x1b[0m"} {
			for n := 0; n <= 3*tWidth; n++ {
				line := []rune(strings.Repeat("abcdefghij", 3*tWidth/10+1)[:n])
				rb := newRedrawTestBuf(tWidth, prompt, line, len(line))

				scr := newTermScreen(tWidth)
				scr.apply(string(rb.redraw(rb.idxLine(tWidth), tWidth)))

				want := strings.TrimRight("> "+string(line), " ")
				if got := scr.text(); got != want {
					t.Fatalf("width %d, prompt %q, %d runes: screen holds\n  %q\nwant\n  %q",
						tWidth, prompt, n, got, want)
				}
			}
		}
	}
}

// And the same for the typing fast path, which goes through append rather than
// redraw and places its own rows with the same helper.
func TestTypingKeepsEveryCharacter(t *testing.T) {
	for _, tWidth := range []int{14, 18, 25, 40} {
		var out bytes.Buffer
		rb := newRedrawTestBuf(tWidth, "> ", nil, 0)
		rb.getConfig().Stdout = &out

		scr := newTermScreen(tWidth)
		rb.Print()
		scr.apply(out.String())

		for n := 1; n <= 3*tWidth; n++ {
			out.Reset()
			rb.WriteRune(rune('a' + (n-1)%26))
			scr.apply(out.String())

			want := strings.TrimRight("> "+string(rb.buf), " ")
			if got := scr.text(); got != want {
				t.Fatalf("width %d, %d runes typed: screen holds\n  %q\nwant\n  %q", tWidth, n, got, want)
			}
		}
	}
}

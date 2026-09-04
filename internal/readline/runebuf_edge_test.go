package readline

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/readline/internal/runes"
)

// termScreen replays the escape sequences a frame emits onto a grid of cells,
// with terminal autowrap ON — the mode the render actually runs under.
//
// The semantics below are not assumed, they were measured against tmux, because
// the interesting state here is one no amount of reading the source predicts.
// A rune written in the last column does NOT move the cursor to the next row:
// it leaves it in a "wrap pending" state, still on the same row, and only the
// *next* rune wraps. Two consequences the tests rely on:
//
//   - \e[A from a wrap-pending cursor moves up from the row it is still on, and
//     tmux clamps it — the render believes it is a row lower than it is, so a
//     frame that walks up by that count lands one row too high and repaints the
//     prompt over the line above. That is the defect that " \b" exists to
//     prevent, by spending a space to force the pending wrap through.
//   - \e[K and \e[J erase from the cursor inclusive, so where the cursor sits
//     when they are emitted decides whether they eat a character.
type termScreen struct {
	rows         [][]rune
	row, col     int // col == width means a wrap is pending
	width        int
	firstDrawRow int
	drew         bool
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
	if s.col >= s.width {
		return // wrap pending: nothing of this row is to the right
	}
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
				s.col = min(s.col, s.width-1)
			case 'B':
				s.row += max(n, 1)
				s.col = min(s.col, s.width-1)
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
		case rs[i] == '\b':
			s.col = max(s.col-1, 0)
			i++
		default:
			w := runes.Width(rs[i])
			if s.col+w > s.width { // the pending wrap fires here, not earlier
				s.row++
				s.col = 0
			}
			if !s.drew {
				s.firstDrawRow, s.drew = s.row, true
			}
			s.at(s.row)[s.col] = rs[i]
			s.col += w
			i++
		}
	}
}

// text reads the grid back as one string, undoing the terminal's row breaks so
// it can be compared against what was meant to be on screen.
func (s *termScreen) text() string {
	var b strings.Builder
	for _, r := range s.rows {
		b.WriteString(string(r))
	}
	return strings.TrimRight(b.String(), " ")
}

// modelCursor is where the cursor model says the cursor is once the whole
// buffer is drawn: the last row getSplitByLine produces, and the column its
// content ends in. A buffer that fills the last column answers "row+1, column
// 0" — the trailing empty row — which is the claim the render has to make true.
func modelCursor(rb *runeBuffer) (row, col int) {
	sp := rb.getSplitByLine(rb.buf, 1)
	row = len(sp) - 1
	col = runes.WidthAll(sp[row])
	if row == 0 {
		col += rb.ppos
	}
	return row, col
}

// TestRedrawLeavesTheCursorWhereTheModelExpects is the property the right-edge
// bug violated. It matters because refresh captures idxLine *before* a mutation
// and the next frame walks up by it: if the terminal and the model disagree
// about which row the cursor is on, every later frame is drawn a row off.
func TestRedrawLeavesTheCursorWhereTheModelExpects(t *testing.T) {
	for _, tWidth := range []int{14, 20, 40} {
		for _, prompt := range []string{"> ", "\x1b[32m> \x1b[0m", "much longer prompt: "} {
			for n := 0; n <= 3*tWidth; n++ {
				line := []rune(strings.Repeat("x", n))
				rb := newRedrawTestBuf(tWidth, prompt, line, len(line))

				scr := newTermScreen(tWidth)
				scr.apply(string(rb.redraw(rb.idxLine(tWidth), tWidth)))

				wantRow, wantCol := modelCursor(rb)
				if scr.row != wantRow || scr.col != wantCol {
					t.Fatalf("width %d, prompt %q, %d runes: frame leaves the cursor at (row %d, col %d), model says (row %d, col %d)",
						tWidth, prompt, n, scr.row, scr.col, wantRow, wantCol)
				}
			}
		}
	}
}

// TestTypingLeavesTheCursorWhereTheModelExpects walks the fast path: typing at
// the end of the buffer never reaches redraw, it goes through append. That is
// where the right edge is normally crossed, so it is where a phantom wrapped
// cell would be left behind.
func TestTypingLeavesTheCursorWhereTheModelExpects(t *testing.T) {
	for _, tWidth := range []int{14, 20, 40} {
		var out bytes.Buffer
		rb := newRedrawTestBuf(tWidth, "> ", nil, 0)
		rb.getConfig().Stdout = &out

		scr := newTermScreen(tWidth)
		rb.Print()
		scr.apply(out.String())

		for n := 1; n <= 3*tWidth; n++ {
			out.Reset()
			rb.WriteRune('x')
			scr.apply(out.String())

			wantRow, wantCol := modelCursor(rb)
			if scr.row != wantRow || scr.col != wantCol {
				t.Fatalf("width %d, %d runes typed: cursor at (row %d, col %d), model says (row %d, col %d)",
					tWidth, n, scr.row, scr.col, wantRow, wantCol)
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
	for _, tWidth := range []int{14, 20, 40} {
		for n := 1; n <= 2*tWidth; n++ {
			line := []rune(strings.Repeat("x", n))
			rb := newRedrawTestBuf(tWidth, "> ", line, len(line))

			first := newTermScreen(tWidth)
			first.row = 1 // start below the top, so a frame drawn too high is visible
			first.apply(string(rb.redraw(rb.idxLine(tWidth), tWidth)))

			// Exactly what refresh does for a Backspace: capture the cursor's row
			// within the drawn block *before* the mutation, then redraw.
			idxLine := rb.idxLine(tWidth)
			rb.buf, rb.idx = line[:n-1], n-1

			second := newTermScreen(tWidth)
			second.row, second.col = first.row, first.col
			second.apply(string(rb.redraw(idxLine, tWidth)))

			if second.firstDrawRow != first.firstDrawRow {
				t.Fatalf("width %d, %d runes: backspace repainted the prompt on row %d, but it was drawn on row %d — the redraw overwrites the line above",
					tWidth, n, second.firstDrawRow, first.firstDrawRow)
			}
		}
	}
}

// TestRenderedRowsKeepEveryCharacter is the screen-level check: \e[K and \e[J
// erase from the cursor inclusive, so a frame can leave the cursor in exactly
// the right place having erased a character on the way there. The cursor checks
// above are blind to that; this one reads the cells back.
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

// And the same for the typing fast path.
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

// TestRedrawLeavesNoLeftovers is what the prompt-row \e[0K used to buy, and the
// reason dropping it from redraw is safe: the trailing \e[J erases from the
// cursor to the end of the display, which covers the rest of the last row as
// well as every row below it. Everything before the cursor was just overwritten
// by this frame, so no cell of a longer previous render can survive.
//
// The shrink is driven the way refresh drives it — idxLine captured before the
// mutation, the second frame starting where the first one left the cursor — so
// a frame that trimmed from the wrong place fails here.
func TestRedrawLeavesNoLeftovers(t *testing.T) {
	for _, tWidth := range []int{14, 20, 40} {
		for _, long := range []int{tWidth / 2, tWidth - 2, tWidth, tWidth + 5, 3 * tWidth} {
			for _, short := range []int{0, 1, tWidth / 3, tWidth - 3} {
				if short >= long {
					continue
				}
				line := []rune(strings.Repeat("z", long))
				rb := newRedrawTestBuf(tWidth, "> ", line, len(line))

				scr := newTermScreen(tWidth)
				scr.apply(string(rb.redraw(rb.idxLine(tWidth), tWidth)))

				idxLine := rb.idxLine(tWidth)
				rb.buf, rb.idx = line[:short], short
				scr.apply(string(rb.redraw(idxLine, tWidth)))

				want := strings.TrimRight("> "+string(line[:short]), " ")
				if got := scr.text(); got != want {
					t.Fatalf("width %d, %d runes shrunk to %d: screen holds\n  %q\nwant\n  %q",
						tWidth, long, short, got, want)
				}
			}
		}
	}
}

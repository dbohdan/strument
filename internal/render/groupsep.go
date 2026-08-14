package render

import (
	"fmt"
	"io"
)

// GroupSep is the blank line between one step of a turn and the next.
//
// A step is a block of thinking and the tool calls it explains, and the
// thinking is what heads it: "let me read the file", then the read. So the
// separator belongs *before* a thinking block rather than after it. It used to
// go after, which grouped each block with the calls above it — the ones it had
// nothing to do with — and in a terminal without faint, where the recessive
// palette does none of the work, that was the only grouping cue there was.
//
// Lazy rather than eager, because the moment a step ends is not a moment the
// harness can see. A step's tool outcomes print after the stream has been
// flushed, and whether another step follows is not known until the next
// request comes back. So nothing is written when a group ends; a debt is
// recorded, and paid by whatever starts the next group. That also means the
// separator can never land at the top of a turn (nothing has drawn, so nothing
// is owed) or at the bottom of one (Clear settles the debt at the boundary).
//
// The zero value is usable and owes nothing, which matters because both
// outputs are built as struct literals — in repl.go, in coder.go, and in every
// test.
type GroupSep struct {
	owed bool
}

// Drew records that something reached the screen, so the next group owes a
// blank line. Callers that draw never pay the debt themselves: consecutive
// outcome lines belong to one group and must not separate each other.
func (g *GroupSep) Drew() { g.owed = true }

// Clear settles the debt without writing, for a caller that has just written a
// blank line of its own or has reached a turn boundary.
func (g *GroupSep) Clear() { g.owed = false }

// Before pays the debt, if there is one, immediately ahead of the group that
// is about to draw.
func (g *GroupSep) Before(w io.Writer) {
	if g.owed {
		fmt.Fprintln(w)
		g.owed = false
	}
}

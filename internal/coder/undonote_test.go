// What /undo tells the model, which is the only thing standing between it and
// building on a file it no longer wrote.

package coder

import (
	"strings"
	"testing"
)

// The note agrees with itself at any file count.
//
// strings.Join welded the verb to the plural, so one file read "widget.go are
// back to what they were" — the common case, and the one where the note most
// needs to sound like it knows what it is talking about, since it appears
// exactly when the user has just thrown work away.
func TestUndoNoteAgreesInNumber(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"widget.go"}, "widget.go is back to what it was"},
		{[]string{"a.go", "b.go"}, "a.go, b.go are back to what they were"},
	} {
		c := testCoder(t)
		c.NoteUndo(tc.files)
		got := c.doneMessages[len(c.doneMessages)-1].Text()
		if !strings.Contains(got, tc.want) {
			t.Errorf("NoteUndo(%v) = %q, want it to contain %q", tc.files, got, tc.want)
		}
	}
}

// The note may not claim the turn was reverted.
//
// A turn is not the unit /undo works in. settleEdits pushes a snapshot per
// commit, so a turn that called commit twice leaves three snapshots and one
// press pops one — by design, so a stopped turn can be reviewed in halves. The
// note used to say "the edits from that turn are gone", which after a
// multi-commit turn is false in the direction that costs something: a model
// told its work was reverted redoes work that is still on disk.
func TestUndoNoteDoesNotClaimTheWholeTurn(t *testing.T) {
	c := testCoder(t)
	c.NoteUndo([]string{"widget.go"})
	got := c.doneMessages[len(c.doneMessages)-1].Text()

	if strings.Contains(got, "from that turn are gone") {
		t.Errorf("the note claims the whole turn was reverted:\n%s", got)
	}
	if !strings.Contains(got, "before that batch is still in place") {
		t.Errorf("the note does not say earlier commits survive:\n%s", got)
	}
	// "that turn" is not resolvable by the reader either: /undo pops the
	// snapshot stack, so the reverted work can be several messages back.
	if strings.Contains(got, "that turn") {
		t.Errorf("the note points at a turn the reader cannot identify:\n%s", got)
	}
}

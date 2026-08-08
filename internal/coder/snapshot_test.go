package coder

import (
	"os"
	"path/filepath"
	"testing"
)

// applyBatch runs one batch of edit-tool calls through the real apply path, the
// way a single send does, so the tests below exercise the seam that actually
// records snapshots.
func applyBatch(t *testing.T, c *Coder, edits ...plannedEdit) {
	t.Helper()
	results := map[string]string{}
	fail := false
	c.applyToolEdits(edits, results, &fail)
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

// TestSnapshotKeepsTheFirstTouch is the rule that makes an undo mean "the
// turn". A turn edits a file across several sends; the state worth keeping is
// the one before the first of them, not before the last.
func TestSnapshotKeepsTheFirstTouch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)

	applyBatch(t, c, plannedEdit{callID: "1", path: "a.txt", search: "one", replace: "two"})
	applyBatch(t, c, plannedEdit{callID: "2", path: "a.txt", search: "two", replace: "three"})
	if got := read(t, dir, "a.txt"); got != "three\n" {
		t.Fatalf("after two batches = %q", got)
	}

	c.pushTurnSnapshot()
	restored, err := c.UndoLastTurn()
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if len(restored) != 1 || restored[0] != "a.txt" {
		t.Errorf("restored = %v", restored)
	}
	if got := read(t, dir, "a.txt"); got != "one\n" {
		t.Errorf("undo left %q, want the state before the turn's first write", got)
	}
}

// TestSnapshotRemovesCreatedFiles: a file the turn brought into existence has
// no previous contents, so undoing it means removing it.
func TestSnapshotRemovesCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)

	applyBatch(t, c, wholeFileWrite("1", "new/deep.txt", "hello\n"))
	if got := read(t, dir, "new/deep.txt"); got != "hello\n" {
		t.Fatalf("write = %q", got)
	}

	c.pushTurnSnapshot()
	if _, err := c.UndoLastTurn(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new/deep.txt")); !os.IsNotExist(err) {
		t.Errorf("the created file survived the undo (err = %v)", err)
	}
}

// TestSnapshotRefusesWhenChangedUnderneath is the safety gate: if the file no
// longer holds what Strument wrote, someone else has been in it, and an undo
// that overwrote them would destroy work no record holds. Nothing is restored,
// not even the files that were untouched — a half-undone turn is worse than
// none.
func TestSnapshotRefusesWhenChangedUnderneath(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("original\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := toolCoder(t, dir)

	applyBatch(t, c,
		plannedEdit{callID: "1", path: "a.txt", search: "original", replace: "edited"},
		plannedEdit{callID: "2", path: "b.txt", search: "original", replace: "edited"},
	)
	c.pushTurnSnapshot()

	// The user opens b.txt in their editor and changes it.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := c.UndoLastTurn(); err == nil {
		t.Fatal("undo went ahead over a file that changed underneath it")
	}
	if got := read(t, dir, "b.txt"); got != "mine\n" {
		t.Errorf("b.txt = %q, want the user's own change untouched", got)
	}
	if got := read(t, dir, "a.txt"); got != "edited\n" {
		t.Errorf("a.txt = %q, want it left alone too — the undo is all or nothing", got)
	}
	if !c.HasTurnSnapshot() {
		t.Error("a refused undo must leave the turn on the stack")
	}
}

// TestSnapshotSkipsAnEmptyTurn keeps /undo pointed at the last turn that
// changed something, rather than at a question the user asked in between.
func TestSnapshotSkipsAnEmptyTurn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)

	applyBatch(t, c, plannedEdit{callID: "1", path: "a.txt", search: "one", replace: "two"})
	c.pushTurnSnapshot()

	c.turnSnap = nil // a read-only turn
	c.pushTurnSnapshot()

	if _, err := c.UndoLastTurn(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got := read(t, dir, "a.txt"); got != "one\n" {
		t.Errorf("a.txt = %q, want the editing turn undone", got)
	}
	if c.HasTurnSnapshot() {
		t.Error("the read-only turn should never have been stacked")
	}
}

// TestSnapshotIgnoresARolledBackBatch: a batch that failed and rolled back
// changed nothing, so it must leave nothing for /undo to unwind.
func TestSnapshotIgnoresARolledBackBatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A regular file where a directory is needed, so the second write fails
	// after the first has already landed.
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("i am a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)

	err := c.writeAtomically(writePlan{
		Writes:     map[string]string{"a.txt": "changed a\n", "blocker/b.txt": "b\n"},
		WriteOrder: []string{"a.txt", "blocker/b.txt"},
	})
	if err == nil {
		t.Fatal("the batch must fail when a target's parent is a regular file")
	}

	c.pushTurnSnapshot()
	if c.HasTurnSnapshot() {
		t.Error("a rolled-back batch left a turn on the undo stack")
	}
	if got := read(t, dir, "a.txt"); got != "original a\n" {
		t.Errorf("a.txt = %q, want the rollback to have restored it", got)
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	if _, err := c.UndoLastTurn(); err == nil {
		t.Error("want an error when the stack is empty")
	}
}

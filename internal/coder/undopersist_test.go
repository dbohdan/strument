package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editTurn drives one turn's worth of writes through the real path so the
// snapshot is built the way a turn builds it, not hand-assembled.
func editTurn(t *testing.T, c *Coder, path, content string) {
	t.Helper()
	res := map[string]string{}
	var reflect bool
	c.applyToolEdits([]plannedEdit{wholeFileWrite("call_1", path, content)}, res, &reflect)
	c.pushTurnSnapshot()
}

// The stack survives the process. Without git this is the only record a turn
// leaves, so losing it on exit was the one place closing a terminal destroyed
// work with nothing said about it.
func TestUndoStackSurvivesAProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	first := toolCoder(t, dir)

	var saved [][]TurnEdit
	first.SaveUndo = func(stack [][]TurnEdit, _ []string, _ string) { saved = stack }

	editTurn(t, first, "a.txt", "one\n")
	editTurn(t, first, "a.txt", "two\n")
	if len(saved) != 2 {
		t.Fatalf("saved %d turns, want 2", len(saved))
	}

	// A second process, same directory.
	second := toolCoder(t, dir)
	second.SetUndoStack(saved)
	if !second.HasTurnSnapshot() {
		t.Fatal("a restored stack must be undoable")
	}

	restored, err := second.UndoLastTurn()
	if err != nil {
		t.Fatalf("undo after restore: %v", err)
	}
	if len(restored) != 1 || restored[0] != "a.txt" {
		t.Errorf("restored = %v", restored)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "one\n" {
		t.Errorf("contents = %q, want the first turn's result back", got)
	}

	// And the turn below it is still reachable, which is what "stack" means.
	if _, err := second.UndoLastTurn(); err != nil {
		t.Errorf("second undo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Error("undoing the turn that created the file should remove it")
	}
}

// Restoring is allowed to be optimistic because the guard is downstream and
// already exists: a file edited by hand between sessions makes UndoLastTurn
// refuse rather than silently discard the edit. This is the property the whole
// feature rests on, so it is pinned here rather than assumed.
func TestRestoredStackStillRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	first := toolCoder(t, dir)
	var saved [][]TurnEdit
	first.SaveUndo = func(stack [][]TurnEdit, _ []string, _ string) { saved = stack }
	editTurn(t, first, "a.txt", "strument wrote this\n")

	// Someone edits the file after Strument exits.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a human wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := toolCoder(t, dir)
	second.SetUndoStack(saved)
	_, err := second.UndoLastTurn()
	if err == nil {
		t.Fatal("undo must refuse a file that changed since Strument wrote it")
	}
	if !strings.Contains(err.Error(), "has changed since Strument wrote it") {
		t.Errorf("err = %v, want the changed-since refusal", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "a human wrote this\n" {
		t.Errorf("the human's edit was not preserved: %q", got)
	}
}

// /undo on a repository gates on the session's own commit hashes. Held only in
// memory, that gate refused a commit Strument had made itself an hour earlier,
// purely because the process had restarted.
func TestSessionCommitsRoundTrip(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.SquashTurns("abc1234", 0) // records the hash; no turns to merge
	c.SquashTurns("def5678", 0)

	hashes, last := c.SessionCommits()
	if len(hashes) != 2 || last != "def5678" {
		t.Fatalf("hashes = %v, last = %q", hashes, last)
	}

	next := toolCoder(t, t.TempDir())
	if next.IsSessionCommit("abc1234") {
		t.Fatal("a fresh coder knows no commits")
	}
	next.RestoreSessionCommits(hashes, last)
	for _, h := range hashes {
		if !next.IsSessionCommit(h) {
			t.Errorf("%s should be a session commit after restore", h)
		}
	}
	if next.LastCommitHash() != "def5678" {
		t.Errorf("last = %q", next.LastCommitHash())
	}
	// The gate is not the only one: a commit Strument never made stays refused.
	if next.IsSessionCommit("0000000") {
		t.Error("restore must not widen the gate")
	}
}

// Every mutation of the stack has to reach disk, or a crash between two of them
// leaves a record that disagrees with the tree. The easiest one to miss is the
// squash that merges nothing but still records its commit.
func TestEveryStackMutationSaves(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	saves := 0
	c.SaveUndo = func([][]TurnEdit, []string, string) { saves++ }

	editTurn(t, c, "a.txt", "one\n") // push
	if saves != 1 {
		t.Fatalf("push: saves = %d, want 1", saves)
	}
	c.DropTurnSnapshot() // the git path's pop
	if saves != 2 {
		t.Errorf("drop: saves = %d, want 2", saves)
	}
	c.SquashTurns("abc1234", 0) // records a hash, merges nothing
	if saves != 3 {
		t.Errorf("squash with nothing to merge must still save the hash: saves = %d", saves)
	}

	editTurn(t, c, "b.txt", "two\n")
	before := saves
	if _, err := c.UndoLastTurn(); err != nil {
		t.Fatal(err)
	}
	if saves != before+1 {
		t.Errorf("undo: saves = %d, want %d", saves, before+1)
	}
}

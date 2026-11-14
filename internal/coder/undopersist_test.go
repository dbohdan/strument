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
	res := toolResults{}
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

	var saved []UndoTurn
	first.SaveUndo = func(stack []UndoTurn, _ string, _ Pending) { saved = stack }

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

	res, err := second.UndoLastTurn()
	if err != nil {
		t.Fatalf("undo after restore: %v", err)
	}
	if len(res.Restored) != 1 || res.Restored[0] != "a.txt" {
		t.Errorf("restored = %v", res.Restored)
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
	var saved []UndoTurn
	first.SaveUndo = func(stack []UndoTurn, _ string, _ Pending) { saved = stack }
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

// Every mutation of the stack has to reach disk, or a crash between two of them
// leaves a record that disagrees with the tree.
func TestEveryStackMutationSaves(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	saves := 0
	c.SaveUndo = func([]UndoTurn, string, Pending) { saves++ }

	editTurn(t, c, "a.txt", "one\n") // push
	if saves != 1 {
		t.Fatalf("push: saves = %d, want 1", saves)
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

// The model's uncommitted work survives the process: the next run's commit
// takes the file the last run edited, and commits the user's own changes to
// it separately first, from the baseline the record carried.
func TestPendingWorkSurvivesAProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	first := toolCoder(t, dir)
	first.Commits = true
	first.Repo = &countingRepo{committingRepo: committingRepo{root: dir, tracked: []string{"a.txt"}}, dirty: true}
	var saved Pending
	first.SaveUndo = func(_ []UndoTurn, _ string, p Pending) { saved = p }
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("the user's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first.initBeforeMessage()
	res := toolResults{}
	var reflect bool
	first.applyToolEdits([]plannedEdit{wholeFileWrite("w1", "a.txt", "the model's\n")}, res, &reflect)
	first.settleEdits()
	if strings.Join(saved.Edits, ",") != "a.txt" || string(saved.Baseline["a.txt"]) != "the user's\n" {
		t.Fatalf("saved = %+v, want a.txt pending with the user's contents as its baseline", saved)
	}

	next := toolCoder(t, dir)
	repo := &countingRepo{committingRepo: committingRepo{root: dir, tracked: []string{"a.txt"}}, dirty: true}
	next.Commits, next.Repo = true, repo
	next.RestorePending(saved)
	next.initBeforeMessage()
	if got := next.runCommitTool(commitArgs{subject: "finish"}); !strings.HasPrefix(got, "Committed ") {
		t.Fatalf("commit = %q", got)
	}
	if len(repo.calls) != 1 || strings.Join(repo.calls[0], ",") != "a.txt" {
		t.Errorf("model commit took %v, want a.txt", repo.calls)
	}
	if len(repo.pre) != 1 || string(repo.pre[0]["a.txt"]) != "the user's\n" {
		t.Errorf("baseline commit = %q, want the user's version, separately", repo.pre)
	}
}

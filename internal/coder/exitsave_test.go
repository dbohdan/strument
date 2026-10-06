package coder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/gitrepo"
)

// An exit in the middle of a turn keeps the turn for /undo and marks it
// uncommitted; the next start commits what is still as the turn left it and
// reports the rest, so an edit made in between never joins a model commit.
func TestExitedTurnIsSavedThenCommittedNextStart(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-qm", "init")

	// The run that exits.
	first := toolCoder(t, dir)
	var savedUncommitted bool
	var saved [][]TurnEdit
	first.SaveUndo = func(stack [][]TurnEdit, _ []string, _ string) {
		saved, savedUncommitted = stack, first.NewestTurnUncommitted()
	}
	first.initBeforeMessage()
	mf := false
	first.applyToolEdits([]plannedEdit{
		wholeFileWrite("w1", "a.go", "package p // edited\n"),
		wholeFileWrite("w2", "b.go", "package p // edited too\n"),
	}, toolResults{}, &mf)
	paths := first.SaveOnExit()
	if strings.Join(paths, ",") != "a.go,b.go" || len(saved) != 1 || !savedUncommitted {
		t.Fatalf("SaveOnExit = %v; saved %d turns, uncommitted %v", paths, len(saved), savedUncommitted)
	}

	// Between runs, someone edits b.go.
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p // a hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The next start.
	next := toolCoder(t, dir)
	repo, err := gitrepo.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Repo, next.AutoCommits = repo, true
	kept, changed, hash, _, err := next.RecoverExited(saved[0])
	if err != nil || strings.Join(kept, ",") != "a.go" || strings.Join(changed, ",") != "b.go" || hash == "" {
		t.Fatalf("RecoverExited = %v, %v, %q, %v", kept, changed, hash, err)
	}
	if files := strings.Fields(git("show", "--name-only", "--format=", "HEAD")); strings.Join(files, ",") != "a.go" {
		t.Errorf("the recovery commit holds %v; the hand-edited b.go must stay out", files)
	}
	if !strings.Contains(git("status", "--porcelain"), "b.go") {
		t.Error("b.go's hand edit should still be uncommitted")
	}
	if !next.IsSessionCommit(hash) {
		t.Error("the recovery commit is not a session commit, so /undo would not take it back")
	}
	last := next.doneMessages[len(next.doneMessages)-1].Content.String()
	if !strings.Contains(last, hash) || !strings.Contains(last, "b.go changed after that run wrote it, and was left") {
		t.Errorf("model note = %q", last)
	}
}

// A double Ctrl-C's first press starts a settle, and the second arrives while
// that settle is mid-commit, holding the lock. The exit must save the turn the
// commit was carrying, at once, rather than wait for a commit it is about to
// kill.
func TestExitDuringACommitSavesItsTurn(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	var saved [][]TurnEdit
	var uncommitted bool
	c.SaveUndo = func(stack [][]TurnEdit, _ []string, _ string) { saved, uncommitted = stack, c.NewestTurnUncommitted() }
	c.initBeforeMessage()
	mf := false
	c.applyToolEdits([]plannedEdit{wholeFileWrite("w1", "a.go", "package p\n")}, toolResults{}, &mf)

	// The settle, frozen mid-commit.
	c.settleMu.Lock()
	c.setSettling(c.turnSnap)

	start := time.Now()
	paths := c.SaveOnExit()
	if took := time.Since(start); took > exitWait/2 {
		t.Errorf("SaveOnExit waited %v for a commit it should not wait for", took)
	}
	if strings.Join(paths, ",") != "a.go" || len(saved) != 1 || !uncommitted || saved[0][0].Path != "a.go" {
		t.Fatalf("paths %v, saved %v, uncommitted %v", paths, saved, uncommitted)
	}
	c.setSettling(nil)
	c.settleMu.Unlock()
}

package coder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A file this turn created can be removed without asking, however the path is
// spelled, and leaves the snapshot so neither the commit nor /undo sees it.
// A file that existed before the turn, a flag other than -f, or anything
// chained still goes through the gate.
func TestRemovingOwnFilesIsANoOp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	rc := &recordingConfirmer{answer: false}
	c.Confirm = rc
	c.SuggestShellCommands = true
	c.initBeforeMessage()
	matchFailure := false
	c.applyToolEdits([]plannedEdit{
		wholeFileWrite("w1", "sim/scratch_test.go", "package sim\n"),
		wholeFileWrite("w2", "keep.go", "package x\n"),
		wholeFileWrite("w3", "old.go", "package x // changed\n"),
	}, toolResults{}, &matchFailure)

	for _, cmd := range []string{
		"rm old.go",                    // existed before the turn
		"rm -r sim",                    // not a plain file removal
		"rm sim/scratch_test.go && ls", // chained
		"rm keep.go missing.go",        // one argument is not the turn's
	} {
		if _, ok := c.removesOnlyOwnFiles(cmd); ok {
			t.Errorf("%q counted as removing only this turn's files", cmd)
		}
	}

	out, ran := c.runShell(context.Background(), toolCommand{command: "rm -f -- ./sim/scratch_test.go", purpose: "tidy"})
	if !ran || len(rc.got) != 0 {
		t.Fatalf("ran=%v, prompts=%d, out %q; want it run unasked", ran, len(rc.got), out)
	}
	if _, err := os.Stat(filepath.Join(dir, "sim", "scratch_test.go")); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
	for _, p := range c.turnSnap.paths() {
		if p == "sim/scratch_test.go" {
			t.Error("the removed file is still in the turn's snapshot, so a commit or /undo would see it")
		}
	}
	if !c.turnSnap.wrote("keep.go") || !c.turnSnap.wrote("old.go") {
		t.Error("removing one file dropped others from the snapshot")
	}

	if _, ran := c.runShell(context.Background(), toolCommand{command: "rm old.go", purpose: "tidy"}); ran || len(rc.got) != 1 {
		t.Errorf("rm of a file older than the turn: ran=%v prompts=%d; want it asked about and declined", ran, len(rc.got))
	}
}

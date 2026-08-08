// Pins the `edited`-vs-rollback invariant at the atomic-apply seam.
// When a multi-file batch write fails, writeAtomically rolls the whole batch
// back and the apply returns an empty `edited`, so the turn neither
// auto-commits nor rotates history. This is the safety-critical divergence
// from aider, whose pre-write `edited` can name files a mid-batch throw never
// touched.
//
// Note the write failure is forced by making a target's parent a regular file.
// Reached by calling applyToolEdits directly: the full Run path's chooseFence
// drops unreadable chat files first, and this container runs as root so
// permission tricks are no-ops — writeAtomically is the last-resort net for
// failures the upstream guards can't foresee (a create/rename race, a full
// disk).
//
// The batch used to arrive as whole-file text listings; it now arrives as write
// tool calls. The invariant is unchanged, and so is the rule the third
// assertion pins: a filesystem failure is *reported* to the model, not turned
// into a reflection, because rewriting the edit cannot fix a full disk.

package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

func toolCoder(t *testing.T, dir string) *Coder {
	t.Helper()
	model := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "tool",
	}
	model.WeakModel = model
	c := New(dir, model)
	c.Confirm = yesConfirmer{}
	c.Out = testOutput{t}
	c.fence = fence{open: "```", close: "```"} // set directly; Run's chooseFence is skipped
	return c
}

// wholeFileWrite is the tool-call form of what used to be a whole-file listing:
// a write call, which lowers to an edit with no search text.
func wholeFileWrite(callID, path, content string) plannedEdit {
	return plannedEdit{callID: callID, path: path, replace: content, create: true}
}

func TestApplyRollbackReturnsEmptyEdited(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `blocker` is a regular file, so writing blocker/b.txt fails at
	// os.MkdirAll after a.txt has already been renamed into place.
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("i am a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")
	c.AddFile("blocker/b.txt")

	results := map[string]string{}
	overwrote := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		wholeFileWrite("call_1", "a.txt", "rewritten a\n"),
		wholeFileWrite("call_2", "blocker/b.txt", "new b\n"),
	}, results, overwrote, &matchFailure)

	if edited != nil {
		t.Errorf("edited = %v on a rolled-back batch, want nil", edited)
	}
	if matchFailure {
		t.Error("a filesystem failure must be reported, not reflected: it is not something the model can fix")
	}
	for _, id := range []string{"call_1", "call_2"} {
		if !strings.Contains(results[id], "rolled back") {
			t.Errorf("result[%s] = %q, want it to say the batch rolled back", id, results[id])
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "original a\n" {
		t.Errorf("a.txt not rolled back: %q", got)
	}
	// blocker/b.txt must not have been created (stat errors with ENOTDIR
	// here since `blocker` is a file, not ENOENT — either way, absent).
	if _, err := os.Stat(filepath.Join(dir, "blocker", "b.txt")); err == nil {
		t.Error("blocker/b.txt should not exist after rollback")
	}
}

func TestWriteAtomicallyRollsBackBatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	plan := writePlan{
		Writes: map[string]string{
			"new.txt":       "created\n",  // did not exist -> rollback removes it
			"existing.txt":  "modified\n", // existed -> rollback restores it
			"blocker/b.txt": "never\n",    // fails: parent is a file
		},
		WriteOrder: []string{"new.txt", "existing.txt", "blocker/b.txt"},
	}

	if err := c.writeAtomically(plan); err == nil {
		t.Fatal("writeAtomically must fail when a target's parent is a file")
	}
	// new.txt was created then rolled back: must be gone.
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt should have been removed on rollback: err=%v", err)
	}
	// existing.txt was overwritten then restored.
	if got, _ := os.ReadFile(filepath.Join(dir, "existing.txt")); string(got) != "original\n" {
		t.Errorf("existing.txt not restored: %q", got)
	}
}

// TestCleanWriteEditedIsWrittenSet is the positive control: a clean
// single-file write writes the file, and `edited` names exactly it.
func TestCleanWriteEditedIsWrittenSet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	c.AddFile("a.txt")

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		wholeFileWrite("call_1", "a.txt", "new content\n"),
	}, results, map[string]string{}, &matchFailure)

	if matchFailure {
		t.Error("unexpected reflection on a clean write")
	}
	if len(edited) != 1 || edited[0] != "a.txt" {
		t.Errorf("edited = %v, want [a.txt]", edited)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "new content\n" {
		t.Errorf("a.txt = %q", got)
	}
}

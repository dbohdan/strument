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
	"runtime"
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
	model.SideModel = model
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

	results := toolResults{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		wholeFileWrite("call_1", "a.txt", "rewritten a\n"),
		wholeFileWrite("call_2", "blocker/b.txt", "new b\n"),
	}, results, &matchFailure)

	if edited != nil {
		t.Errorf("edited = %v on a rolled-back batch, want nil", edited)
	}
	if matchFailure {
		t.Error("a filesystem failure must be reported, not reflected: it is not something the model can fix")
	}
	for _, id := range []string{"call_1", "call_2"} {
		if !strings.Contains(results[id].Text, "rolled back") {
			t.Errorf("result[%s] = %q, want it to say the batch rolled back", id, results[id].Text)
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

	results := toolResults{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		wholeFileWrite("call_1", "a.txt", "new content\n"),
	}, results, &matchFailure)

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

// A write onto a directory must fail without taking the directory with it.
// Existence used to come from whether the target could be *read*, so a
// directory counted as "did not exist" and the rollback removed it — an empty
// one went, silently, under an error about the rename.
func TestWriteAtomicallyLeavesADirectoryTargetAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	plan := writePlan{
		Writes:     map[string]string{"new.txt": "created\n", "d": "x\n"},
		WriteOrder: []string{"new.txt", "d"},
	}
	if err := c.writeAtomically(plan); err == nil {
		t.Fatal("writing onto a directory must fail")
	}
	if fi, err := os.Stat(filepath.Join(dir, "d")); err != nil || !fi.IsDir() {
		t.Errorf("the directory must survive the failed write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("the rest of the batch must still roll back: err=%v", err)
	}
}

// A file that exists but cannot be read cannot be snapshotted, so writing it
// would be a write /undo cannot take back: recorded as "did not exist", undo
// deleted it. It is refused before anything is touched.
func TestWriteAtomicallyRefusesAFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0200 file anyway")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.WriteFile(locked, []byte("theirs\n"), 0o200); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	err := c.writeAtomically(writePlan{Writes: map[string]string{"locked": "ours\n"}, WriteOrder: []string{"locked"}})
	if err == nil {
		t.Fatal("a file whose contents cannot be kept for undo must not be overwritten")
	}
	_ = os.Chmod(locked, 0o600)
	if got, _ := os.ReadFile(locked); string(got) != "theirs\n" {
		t.Errorf("the file changed: %q", got)
	}
}

package coder

import (
	"os"
	"path/filepath"
	"strings"
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

// TestEditPreservesFileMode: a rename swaps inodes, so without care every edit
// left the file at 0o644 — scripts stopped being executable and a 0o600 file
// came back world-readable. Changing contents is what was asked for; changing
// who can read or run the file was not.
func TestEditPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		mode os.FileMode
	}{
		{"script.sh", 0o755},
		{"secret.env", 0o600},
		{"plain.txt", 0o644},
	} {
		if err := os.WriteFile(filepath.Join(dir, c.name), []byte("before\n"), c.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, c.name), c.mode); err != nil { // defeat umask
			t.Fatal(err)
		}
	}

	cdr := toolCoder(t, dir)
	applyBatch(t, cdr,
		plannedEdit{callID: "1", path: "script.sh", search: "before", replace: "after"},
		plannedEdit{callID: "2", path: "secret.env", search: "before", replace: "after"},
		plannedEdit{callID: "3", path: "plain.txt", search: "before", replace: "after"},
	)

	for name, want := range map[string]os.FileMode{
		"script.sh": 0o755, "secret.env": 0o600, "plain.txt": 0o644,
	} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %v, want %v", name, got, want)
		}
		if got := read(t, dir, name); got != "after\n" {
			t.Errorf("%s = %q, want the edit applied", name, got)
		}
	}
}

// TestNewFileGetsTheDefaultMode pins the other half: a file the turn creates
// has no previous mode to keep.
func TestNewFileGetsTheDefaultMode(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	applyBatch(t, c, wholeFileWrite("1", "fresh.txt", "hello\n"))

	fi, err := os.Stat(filepath.Join(dir, "fresh.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != newFileMode {
		t.Errorf("new file mode = %v, want %v", got, os.FileMode(newFileMode))
	}
}

// TestEditWritesThroughASymlink: a rename replaces the link rather than
// following it, so before this the edit turned the symlink into a regular file
// and left the real file untouched — the change landed nowhere the user was
// looking.
func TestEditWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := toolCoder(t, dir)
	applyBatch(t, c, plannedEdit{callID: "1", path: "link.txt", search: "before", replace: "after"})

	if got := read(t, dir, "real.txt"); got != "after\n" {
		t.Errorf("real.txt = %q, want the edit to have reached it through the link", got)
	}
	fi, err := os.Lstat(filepath.Join(dir, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("link.txt is no longer a symlink")
	}
}

// TestUndoLeavesAModeTheUserChanged: the turn never changed the mode, so the
// undo has no business changing it either. A chmod between the turn and the
// undo belongs to whoever ran it.
func TestUndoLeavesAModeTheUserChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	applyBatch(t, c, plannedEdit{callID: "1", path: "a.txt", search: "one", replace: "two"})
	c.pushTurnSnapshot()

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UndoLastTurn(); err != nil {
		t.Fatalf("undo: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after undo = %v, want the user's 0600 kept", got)
	}
	if got := read(t, dir, "a.txt"); got != "one\n" {
		t.Errorf("contents = %q, want them reverted", got)
	}
}

// TestUndoRollsBackWhenARestoreFails is the all-or-nothing promise under a
// failure the pre-flight cannot see. A directory standing where a file was
// makes the write fail as any user, root included — which is what this
// container runs as, so an EACCES test would silently pass without proving
// anything.
func TestUndoRollsBackWhenARestoreFails(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := toolCoder(t, dir)
	applyBatch(t, c,
		plannedEdit{callID: "1", path: "a.txt", search: "one", replace: "two"},
		plannedEdit{callID: "2", path: "b.txt", search: "one", replace: "two"},
	)
	c.pushTurnSnapshot()

	// b.txt becomes a directory: reading it fails, and so does writing it.
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "b.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := c.UndoLastTurn(); err == nil {
		t.Fatal("want an error when a restore cannot be completed")
	}
	if got := read(t, dir, "a.txt"); got != "two\n" {
		t.Errorf("a.txt = %q, want the turn's own result put back", got)
	}
	if !c.HasTurnSnapshot() {
		t.Error("a failed undo must leave the turn on the stack")
	}
}

// TestParseWarningOnRegression: the edit still applies — this is a warning, not
// a gate — but the model is told in the same step, and so is the user.
func TestParseWarningOnRegression(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package a\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &captureOut{}
	c := toolCoder(t, dir)
	c.Out = out

	results := map[string]string{}
	fail := false
	c.applyToolEdits([]plannedEdit{
		{callID: "1", path: "a.go", search: "func F() int { return 1 }\n", replace: "func F() int { return 1\n"},
	}, results, &fail)

	if got := read(t, dir, "a.go"); !strings.Contains(got, "return 1\n") {
		t.Errorf("the edit did not apply: %q", got)
	}
	if !strings.Contains(results["1"], "no longer does") {
		t.Errorf("the model was not told:\n%s", results["1"])
	}
	if joined := strings.Join(out.lines, "\n"); !strings.Contains(joined, "no longer does") {
		t.Errorf("the user was not told:\n%s", joined)
	}
}

// TestNoParseWarningWhenAlreadyBroken: the model may be mid-repair, and a note
// about breakage it did not cause is noise it cannot act on.
func TestNoParseWarningWhenAlreadyBroken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package a\n\nfunc F() int { return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	results := map[string]string{}
	fail := false
	c.applyToolEdits([]plannedEdit{
		{callID: "1", path: "a.go", search: "func F() int { return 1\n", replace: "func F() int { return 2\n"},
	}, results, &fail)

	if strings.Contains(results["1"], "no longer does") {
		t.Errorf("warned about a file that was already broken:\n%s", results["1"])
	}
}

// TestNoParseWarningWithoutAGrammar: silence here means "nothing is known",
// which is not the same as "the file is fine".
func TestNoParseWarningWithoutAGrammar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	results := map[string]string{}
	fail := false
	c.applyToolEdits([]plannedEdit{
		{callID: "1", path: "notes.txt", search: "one\n", replace: "}}} not code {{{\n"},
	}, results, &fail)

	if strings.Contains(results["1"], "no longer does") {
		t.Errorf("warned about a file no grammar covers:\n%s", results["1"])
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	if _, err := c.UndoLastTurn(); err == nil {
		t.Error("want an error when the stack is empty")
	}
}

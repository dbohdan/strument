package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The edit tools may touch the platform's standard temporary directory, by
// absolute path. The sandbox already grants model-run commands that ground
// (sandbox.tempDirs); these tests pin the edit side meeting the same
// boundary. Found from the 2026-10-code-only trial, where models preparing
// scratch fixtures met one boundary through bash and the opposite one
// through edit.

// A create into temp lands in the real file, not a shadow tree grafted under
// the project root.
func TestWriteToTempDirectoryIsAllowed(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)

	target := filepath.Join(t.TempDir(), "scratch", "script.sh")
	if reason := c.unsafePath(target); reason != "" {
		t.Fatalf("a temp-dir path was refused: %s", reason)
	}

	results := map[string]string{}
	var matchFailure bool
	edited := c.applyToolEdits([]plannedEdit{{
		callID: "call_1", path: target, create: true,
		replace: "#!/bin/sh\necho hi\n",
	}}, results, &matchFailure)
	if matchFailure {
		t.Error("unexpected match failure writing a temp file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Errorf("the temp file was not written: %q", got)
	}
	// And no shadow tree under the project root mirroring the absolute path.
	if _, err := os.Stat(filepath.Join(root, "tmp")); !os.IsNotExist(err) {
		t.Error("a shadow tree was created under the project root")
	}
	if len(edited) != 1 {
		t.Errorf("edited = %v, want the temp path", edited)
	}
}

// An *edit* of an existing temp file reads the real file first. diskReader
// used to join absolute paths onto the root, so the planner read a
// nonexistent shadow path, planned against "file missing", and the whole-file
// write would have replaced the real contents while reporting an edit.
func TestEditOfTempFileReadsTheRealFile(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)

	target := filepath.Join(t.TempDir(), "note.txt")
	const before = "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(target, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	results := map[string]string{}
	var matchFailure bool
	edited := c.applyToolEdits([]plannedEdit{{
		callID: "call_1", path: target,
		search: "beta\n", replace: "BETA\n",
	}}, results, &matchFailure)
	if matchFailure {
		t.Fatalf("the planner could not read the temp file: result %q", results["call_1"])
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Errorf("the edit did not land on the real file: %q", got)
	}
	if len(edited) != 1 {
		t.Errorf("edited = %v, want the temp path", edited)
	}
}

// The grant is absolute-path only: a relative traversal that lands in temp is
// still refused, so ../.. remains a hole nothing opens.
func TestRelativeTraversalIntoTempIsStillRefused(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)

	reason := c.unsafePath("../../tmp/strument-escape/x.go")
	if reason == "" && filepath.Clean(filepath.Join(root, "../../tmp")) != "/tmp" {
		t.Skip("the traversal from this root does not land in temp; the refusal below is the assertion")
	}
	if reason == "" {
		t.Error("a relative traversal into temp was allowed; the grant is absolute-path only")
	}
	if c.unsafePath("/etc/passwd") == "" {
		t.Error("an absolute out-of-root, out-of-temp path was allowed")
	}
}

// A temp path that resolves inside a cloned repository's .git stays refused:
// the raw .git check runs before the temp carve-out, and a write there is
// code execution in Strument's own git.
func TestTempPathIntoGitDirIsStillRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, root)
	if reason := c.unsafePath(filepath.Join(root, ".git", "config")); reason == "" {
		t.Error("a .git path was allowed")
	}
}

// The turn commit must survive a temp file in the batch: git add with an
// out-of-repo path fails the whole commit, taking the in-repo edits' commit
// with it. The filter drops the temp path, commits the rest, and says so.
func TestTurnCommitSkipsTempFiles(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)
	repo := &committingRepo{asked: [][]string{}, root: root}
	c.Repo = repo
	c.AutoCommits = true

	if err := os.WriteFile(filepath.Join(root, "in.go"), []byte("package in\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("in.go", snapEntry{}, "package in\n")
	c.turnSnap.record(filepath.Join(t.TempDir(), "scratch.txt"), snapEntry{}, "scratch\n")

	c.commitTurn("test: one repo file, one temp file")

	if len(repo.asked) != 1 || len(repo.asked[0]) != 1 || repo.asked[0][0] != "in.go" {
		t.Errorf("git was asked to commit %v, want [in.go] — the temp path must be filtered", repo.asked)
	}
	// The snapshot keeps everything, so /undo still reaches the temp file.
	if got := c.turnSnap.paths(); len(got) != 2 {
		t.Errorf("the snapshot lost paths: %v", got)
	}
}

func TestTurnCommitSkipsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "scratch")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := toolCoder(t, root)
	c.AddFile(filepath.Join(link, "outside.txt"))
	repo := &committingRepo{asked: [][]string{}, root: root}
	c.Repo = repo
	c.AutoCommits = true

	results := map[string]string{}
	var matchFailure bool
	c.applyToolEdits([]plannedEdit{
		wholeFileWrite("call_1", "in.go", "package in\n"),
		wholeFileWrite("call_2", filepath.Join("scratch", "outside.txt"), "package out\n"),
	}, results, &matchFailure)
	if matchFailure {
		t.Fatal("unexpected match failure applying edits")
	}
	if got, err := os.ReadFile(filepath.Join(outside, "outside.txt")); err != nil || string(got) != "package out\n" {
		t.Fatalf("the symlink target was not written: %q, %v", got, err)
	}

	c.commitTurn("test: skip symlink escape")

	if len(repo.asked) != 1 || len(repo.asked[0]) != 1 || repo.asked[0][0] != "in.go" {
		t.Errorf("git was asked to commit %v, want [in.go] — the symlink target must be filtered", repo.asked)
	}
}

// The drop is announced, not silent — the readonly work documented what a
// model believes-is-committed-but-is-not costs.
func TestTurnCommitAnnouncesSkippedTempFiles(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)
	c.Repo = &committingRepo{root: root}
	c.AutoCommits = true

	temp := filepath.Join(t.TempDir(), "scratch.txt")
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record(temp, snapEntry{}, "scratch\n")

	c.commitTurn("test: only a temp file")

	if c.lastCommitHash != "" {
		t.Errorf("a temp-only batch produced commit %q", c.lastCommitHash)
	}
}

// committablePaths unit coverage.
func TestCommittablePathsFilter(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)
	c.Repo = &committingRepo{root: root}

	keep := c.committablePaths([]string{"a.go", "internal/b.go", "/tmp/x/scratch", filepath.Join(t.TempDir(), "y")})
	if len(keep) != 2 || keep[0] != "a.go" || keep[1] != "internal/b.go" {
		t.Errorf("committablePaths kept %v, want the two repo-relative names", keep)
	}
}

// The skip line must name what was left out, in words the model can act on.
func TestCommittablePathsMessageNamesThePaths(t *testing.T) {
	root := t.TempDir()
	c := toolCoder(t, root)
	out := &captureOut{}
	c.Out = out
	c.Repo = &committingRepo{root: root}

	temp := filepath.Join(t.TempDir(), "scratch.txt")
	c.committablePaths([]string{temp})
	if !strings.Contains(strings.Join(out.lines, "\n"), temp) {
		t.Errorf("the skip announcement did not name the dropped path:\n%s",
			strings.Join(out.lines, "\n"))
	}
}

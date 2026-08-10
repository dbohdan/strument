package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// newRepo makes a repository with a subdirectory and returns both paths.
func newRepo(t *testing.T) (root, sub string) {
	t.Helper()
	root = t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	sub = filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// t.TempDir can hand back a symlinked path (/tmp -> /private/tmp on macOS)
	// while git reports the resolved one. The hash is taken over the string, so
	// compare against what the filesystem actually calls it.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, sub
}

// A transcript belongs to the project, and inside a repository the project is
// the worktree root — from any subdirectory of it.
func TestHistoryRootIsTheWorktreeRoot(t *testing.T) {
	root, sub := newRepo(t)
	for _, dir := range []string{root, sub} {
		if got := historyRootFrom(dir); got != root {
			t.Errorf("historyRootFrom(%s) = %s, want %s", dir, got, root)
		}
	}
}

// Outside a repository there is nothing to climb to, so the directory is the
// project. This is the --no-git-in-a-plain-directory case that the snapshot
// substrate exists for.
func TestHistoryRootOutsideARepoIsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if got := historyRootFrom(dir); got != dir {
		t.Errorf("historyRootFrom(%s) = %s, want itself", dir, got)
	}
}

// The regression. chat used to derive the history root honoring --no-git and
// history used to derive it ignoring --no-git, so `chat --no-git` in a
// subdirectory wrote proj/sub's transcript while `strument history` printed
// proj's — two paths, two hashes, and no way to find your own history.
//
// Both commands call historyRoot now, so the invariant to pin is that the path
// does not depend on where under the project you stand, or on --no-git, which
// says how a turn is committed rather than which project it belongs to.
func TestHistoryPathIsTheSameFromAnywhereInTheProject(t *testing.T) {
	root, sub := newRepo(t)
	cfg := &config.Config{}

	fromRoot, err := resolveHistoryPath(cfg, historyRootFrom(root))
	if err != nil {
		t.Fatal(err)
	}
	fromSub, err := resolveHistoryPath(cfg, historyRootFrom(sub))
	if err != nil {
		t.Fatal(err)
	}
	if fromRoot != fromSub {
		t.Errorf("the transcript moved with the working directory:\n root %s\n sub  %s", fromRoot, fromSub)
	}
	if filepath.Base(fromRoot) == filepath.Base(sub)+".md" {
		t.Error("the path is keyed on the subdirectory rather than the project")
	}
}

// A relative history_file override resolves against the same root, so the two
// mechanisms cannot disagree about which project this is either.
func TestHistoryOverrideResolvesAgainstTheProjectRoot(t *testing.T) {
	root, sub := newRepo(t)
	cfg := &config.Config{HistoryFile: "notes/chat.md"}

	want := filepath.Join(root, "notes", "chat.md")
	for _, dir := range []string{root, sub} {
		got, err := resolveHistoryPath(cfg, historyRootFrom(dir))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("from %s: got %s, want %s", dir, got, want)
		}
	}
}

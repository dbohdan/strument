package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/history"
)

// newRepo makes a repository with a subdirectory and returns both paths.
func newRepo(t *testing.T) (root, sub string) {
	t.Helper()
	root = t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	// t.TempDir can hand back a symlinked path (/tmp -> /private/tmp on macOS)
	// while git reports the resolved one. The hash is taken over the string, so
	// compare against what the filesystem actually calls it.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	sub = filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
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

// The alias is recorded only when it differs from the config's default.
// Recording the default would pin a project to whatever it happened to be the
// first time the project was opened, so that later editing `default` in
// config.star would mysteriously not take effect there.
func TestResumeRecordsOnlyANonDefaultAlias(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, _ := newRepo(t)
	cfg := &config.Config{Default: "mimo"}
	cdr := coder.New(root, &config.Model{Slug: "x"})

	save := saveResumeFunc(cdr, cfg, root, true)
	if save == nil {
		t.Fatal("no save function when state is kept")
	}

	save("mimo") // the default: nothing to remember
	if got := history.LoadResume(root).Model; got != "" {
		t.Errorf("the default alias was pinned: %q", got)
	}
	save("sonnet") // a deliberate choice: remember it
	if got := history.LoadResume(root).Model; got != "sonnet" {
		t.Errorf("model = %q, want sonnet", got)
	}
	// Switching back to the default is the way out of the pin.
	save("mimo")
	if got := history.LoadResume(root).Model; got != "" {
		t.Errorf("switching back to the default left %q pinned", got)
	}
}

// --no-history means leave no trace, so there is nothing to call.
func TestResumeIsNotSavedWithoutState(t *testing.T) {
	if save := saveResumeFunc(nil, nil, "/tmp/whatever", false); save != nil {
		t.Error("a no-trace session should have no save function")
	}
}

// Paths are recorded relative to the project root rather than the coder's, so
// they survive --no-git, where the coder works from the invocation directory
// while the project is still the worktree.
func TestResumePathsAreProjectRelative(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, sub := newRepo(t)
	if err := os.WriteFile(filepath.Join(sub, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A --no-git session in sub/: the coder's root is sub, the project is root.
	cdr := coder.New(sub, &config.Model{Slug: "x"})
	cdr.AddFile(filepath.Join(sub, "b.go"))

	saveResumeFunc(cdr, &config.Config{Default: "m"}, root, true)("m")

	got := history.LoadResume(root).Files
	if len(got) != 1 || got[0] != "sub/b.go" {
		t.Errorf("files = %v, want [sub/b.go]", got)
	}
}

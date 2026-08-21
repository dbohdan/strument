package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The writable set is the entire security decision, so it is worth testing
// exhaustively — and it is pure, so it can be, on any OS and without a kernel.

func has(t *testing.T, paths []string, want string) bool {
	t.Helper()
	abs, _ := filepath.Abs(want)
	return slices.Contains(paths, abs)
}

func TestDefaultWritableCoversASession(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	project, state := t.TempDir(), t.TempDir()

	got := DefaultWritable(project, state, []string{"/extra"})

	for _, want := range []string{project, state, os.TempDir(), os.Getenv("XDG_CACHE_HOME"), "/extra"} {
		if !has(t, got, want) {
			t.Errorf("%s is not writable; a session needs it:\n%v", want, got)
		}
	}
}

// TestDefaultWritableKeepsGoBinReadOnly pins the one path in the cache group
// that must not be widened. GOPATH holds bin/ beside pkg/, and a writable
// ~/go/bin is a way to replace a binary the user runs later — the durable
// foothold this policy exists to deny.
func TestDefaultWritableKeepsGoBinReadOnly(t *testing.T) {
	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath)

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)

	if !has(t, got, filepath.Join(gopath, "pkg")) {
		t.Errorf("the module cache is not writable; `go build` would fail:\n%v", got)
	}
	if has(t, got, gopath) {
		t.Errorf("all of GOPATH is writable, which includes bin/:\n%v", got)
	}
	if has(t, got, filepath.Join(gopath, "bin")) {
		t.Error("GOPATH/bin is writable")
	}
}

// TestDefaultWritableHonoursMovedCaches: a machine that has relocated its
// cache is respected, rather than having the default granted and the real one
// silently denied.
func TestDefaultWritableHonoursMovedCaches(t *testing.T) {
	moved := t.TempDir()
	t.Setenv("GOCACHE", moved)

	if got := DefaultWritable(t.TempDir(), t.TempDir(), nil); !has(t, got, moved) {
		t.Errorf("a relocated GOCACHE was not granted:\n%v", got)
	}
}

// TestGitDirForAWorktree: in a worktree .git is a *file* pointing outside the
// project, and Strument commits every turn. Granting only the project root
// would make every commit fail in a way that looks like a git bug.
func TestGitDirForAWorktree(t *testing.T) {
	main := t.TempDir()
	gitdir := filepath.Join(main, ".git")
	worktreeGit := filepath.Join(gitdir, "worktrees", "feature")
	if err := os.MkdirAll(worktreeGit, 0o700); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: "+worktreeGit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := gitDir(project); got != gitdir {
		t.Errorf("gitDir = %q, want the whole %q — git writes shared refs and objects there, not just the worktree's subdirectory", got, gitdir)
	}
	if !has(t, DefaultWritable(project, t.TempDir(), nil), gitdir) {
		t.Error("a worktree's git directory is not writable; every commit would fail")
	}
}

// TestGitDirForAPlainRepository adds nothing: .git is inside the project and
// already covered by the project rule.
func TestGitDirForAPlainRepository(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := gitDir(project); got != "" {
		t.Errorf("gitDir = %q for an ordinary repository, want none", got)
	}
}

func TestGitDirIgnoresRubbish(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("not a gitdir line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gitDir(project); got != "" {
		t.Errorf("gitDir = %q for a .git file with no gitdir: line, want none", got)
	}
}

// TestDefaultWritableIsDeduped: Landlock takes the rules as given, and the same
// directory arriving twice is noise in the ruleset and in any message that
// lists the writable roots to a user.
func TestDefaultWritableIsDeduped(t *testing.T) {
	project := t.TempDir()
	got := DefaultWritable(project, project, []string{project, project + "/"})

	count := 0
	for _, p := range got {
		if abs, _ := filepath.Abs(project); p == abs {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the project appears %d times:\n%v", count, got)
	}
}

// TestDefaultWritableNeverGrantsTheWholeHome is the test that would catch the
// worst possible regression: a cache default resolving to $HOME itself gives
// away the entire home directory, which is the one thing this feature exists
// to prevent.
func TestDefaultWritableNeverGrantsTheWholeHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		t.Skip("no usable home directory here")
	}
	for _, p := range DefaultWritable(t.TempDir(), t.TempDir(), nil) {
		if p == filepath.Clean(home) {
			t.Fatalf("the whole home directory is writable, via %q", p)
		}
		if p == "/" || p == filepath.VolumeName(p)+string(filepath.Separator) {
			t.Fatalf("a filesystem root is writable, via %q", p)
		}
	}
}

// TestDefaultWritableSkipsEmptyEntries: an unset environment variable must not
// become a rule, least of all one for the current working directory.
func TestDefaultWritableSkipsEmptyEntries(t *testing.T) {
	for _, p := range DefaultWritable("", "", []string{""}) {
		if p == "" || !filepath.IsAbs(p) {
			t.Errorf("a non-absolute or empty path reached the ruleset: %q", p)
		}
	}
}

// TestTempDirsAlwaysIncludeSlashTmp: plenty of tools write to /tmp whatever
// TMPDIR says, and denying it would break them while protecting nothing —
// /tmp is world-writable already, and this policy protects the user's files.
func TestTempDirsAlwaysIncludeSlashTmp(t *testing.T) {
	moved := t.TempDir()
	t.Setenv("TMPDIR", moved)

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)
	if !has(t, got, moved) {
		t.Errorf("TMPDIR was not granted:\n%v", got)
	}
	if !has(t, got, "/tmp") {
		t.Errorf("/tmp was not granted alongside a relocated TMPDIR:\n%v", got)
	}
}

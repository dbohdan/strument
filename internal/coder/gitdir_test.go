// The edit path and .git: the hole this closes, stated as the test that failed.

package coder

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/gitrepo"
)

// gitDirCoder builds a coder over a real repository, because the refusal has to
// survive the checks that ran before this one existed: git check-ignore reports
// .git/config as *not* ignored, so allowedToEdit's gitignore refusal never saw
// it either.
func gitDirCoder(t *testing.T) *Coder {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	c := toolCoder(t, dir)
	repo, err := gitrepo.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	c.Repo = repo
	return c
}

// What a write to .git/config buys, and why no prompt stands in the way:
// core.fsmonitor (or core.pager, core.sshCommand, an alias) runs on the next
// ordinary git command, Strument runs those constantly, and Strument's own git
// deliberately keeps the whole environment — OPENROUTER_API_KEY included. The
// edit tool asks nothing before writing, by design, and .git is untracked so
// the write never appears in `git show`.
func TestEditPathRefusesGitDir(t *testing.T) {
	c := gitDirCoder(t)

	for _, p := range []string{
		".git/config",
		".git/hooks/pre-commit",
		".GIT/config",
		`.git\hooks\post-commit`,
		".git/info/exclude",
	} {
		if reason := c.unsafePath(p); reason == "" {
			t.Errorf("unsafePath(%q) = \"\" — the model may write the repository's own internals", p)
		}
	}
}

// Pinning does not unlock it: isPinned's result is fed to Workspace as a
// containment exemption, so if an edit could add to absFnames the model could
// widen its own permissions one edit at a time. It cannot; only /add writes
// there.
func TestEditPathRefusesGitDirEvenWhenPinned(t *testing.T) {
	c := gitDirCoder(t)
	full := filepath.Join(c.Root, ".git", "config")
	c.absFnames = append(c.absFnames, full)
	c.absReadOnlyFnames = append(c.absReadOnlyFnames, full)

	if reason := c.unsafePath(".git/config"); reason == "" {
		t.Error("a pinned .git/config was accepted for editing")
	}
}

// The counter-test: ordinary dot-files stay editable. A guard that refused
// .github or .gitignore would break the harness editing its own CI config,
// which is a thing it is explicitly meant to do.
func TestEditPathStillAllowsOrdinaryDotFiles(t *testing.T) {
	c := gitDirCoder(t)
	if err := os.MkdirAll(filepath.Join(c.Root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{".gitignore", ".github/workflows/ci.yml", ".gitmodules", "src/.gitkeep"} {
		if reason := c.unsafePath(p); reason != "" {
			t.Errorf("unsafePath(%q) = %q — an ordinary project file was refused", p, reason)
		}
	}
}

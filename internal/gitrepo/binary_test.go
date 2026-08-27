package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolvedBinarySurvivesAPathChange is the property env_set made necessary:
// once git is resolved, changing PATH does not change which binary Strument's
// own git invocations run. That matters because those invocations inherit the
// whole environment, API key included, while everything the model causes goes
// through FilterEnv.
//
// The shim is a real executable that would answer rev-parse plausibly, so a
// regression shows up as the wrong answer rather than as a crash — a shim that
// merely failed could be mistaken for "no git here" and pass by accident.
func TestResolvedBinarySurvivesAPathChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shim; the resolution itself is platform-independent")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	// Pinning is process-wide, and TestCommitSignFlag shims git through PATH to
	// capture argv — the exact interposition this defeats. Restore it, or this
	// test decides that one's result by running first.
	saved := pinnedGit
	t.Cleanup(func() { pinnedGit = saved })

	// Resolve from the real PATH, as main does before reading any config.
	ResolveBinary()
	if !filepath.IsAbs(gitBinary()) {
		t.Fatalf("git resolved to %q, want an absolute path", gitBinary())
	}

	repo := t.TempDir()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(realGit, "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	// A hostile PATH holding nothing but a shim that claims a different root.
	shimDir := t.TempDir()
	hijacked := filepath.Join(shimDir, "hijacked")
	shim := "#!/bin/sh\necho " + hijacked + "\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir)

	// A bare-name lookup would find the shim; the resolved path must not.
	if p, err := exec.LookPath("git"); err != nil || filepath.Dir(p) != shimDir {
		t.Fatalf("the shim is not what a fresh lookup finds (%q, %v); the test proves nothing", p, err)
	}

	got, err := Discover(repo)
	if err != nil {
		t.Fatalf("Discover after the PATH change: %v", err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root() != want {
		t.Errorf("Discover used the hijacked git: root = %q, want %q", got.Root(), want)
	}
}

package coder

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/gitrepo"
)

// TestCommitTurnNoopAfterToolCommit pins the message that ends a turn whose
// writes netted to zero after the turn already committed. The pre-commit-tool
// wording — "the turn left the files as they were" — is false the moment the
// turn holds a commit, and it was the model's commits that made that ordinary:
// a part-one commit followed by part-two edits that net out is normal commit
// tool usage, not a failure.
//
// Real repository on purpose: the no-op is decided by git status, not by the
// snapshot, and a stub repo answers ok=true unconditionally.
func TestCommitTurnNoopAfterToolCommit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
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
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")

	c := toolCoder(t, dir)
	out := &captureOut{}
	c.Out = out
	c.Commits = true
	repo, err := gitrepo.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	c.Repo = repo

	// Part one: an edit and its commit, the way the commit tool produces one.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one\n")
	c.runCommitTool(commitArgs{subject: "part one"})
	if c.lastCommitHash == "" {
		t.Fatal("part one did not commit; the test's premise is broken")
	}

	// Part two nets to zero: rewritten to exactly the committed content.
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one\n")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.runCommitTool(commitArgs{subject: "part two"})

	// The announced no-op must reference the commit that stands, not claim
	// the turn changed nothing.
	want := "Nothing to commit since " + c.lastCommitHash[:7] + "."
	found := false
	for _, line := range out.lines {
		if strings.Contains(line, want) {
			found = true
		}
		if strings.Contains(line, "left the files as they were") {
			t.Errorf("the pre-commit-tool wording fired although the turn holds commit %s:\n%s",
				c.lastCommitHash, line)
		}
	}
	if !found {
		t.Errorf("no \"Nothing to commit since %s\" announcement; got:\n%s",
			c.lastCommitHash[:7], strings.Join(out.lines, "\n"))
	}
}

// The commit-message input used to have no bound at all: gitrepo passes
// `git diff --cached` through verbatim and renderCommitMessages writes every
// message's full text, so the call grew with the turn until a provider refused
// it. These pin the bound and, more importantly, pin *what gives* when the
// input does not fit.

// A commit that git refuses — a pre-commit hook is the everyday case — is not
// "nothing to commit". The commit tool inferred that from the hash not moving
// and told the model "the files match what is already committed", while the
// hook's reason went only to the screen; and settling cleared the turn's
// pending writes anyway, so a retry was told nothing had been written and the
// edits were never committed at all.
func TestCommitToolReportsARefusedCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell script")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'lint: trailing whitespace in a.go' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.Out = &captureOut{}
	c.Commits = true
	repo, err := gitrepo.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Repo = repo
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one \n")

	got := c.runCommitTool(commitArgs{subject: "add a"})
	if strings.Contains(got, "match what is already committed") {
		t.Errorf("result = %q; a refused commit was reported as having nothing to commit", got)
	}
	if !strings.Contains(got, "trailing whitespace") {
		t.Errorf("result = %q, want the hook's reason, which is what the model can fix", got)
	}

	// The hook is satisfied; the same edits must still be there to commit.
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if got := c.runCommitTool(commitArgs{subject: "add a"}); !strings.HasPrefix(got, "Committed ") {
		t.Errorf("retry = %q; the refused commit's edits were dropped from what is pending", got)
	}
}

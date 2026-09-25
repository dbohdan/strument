package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each file is named in the prompt only when it is pinned, and the private one
// is told apart from the shared one.
func TestAgentsLocalIsNamedInThePrompt(t *testing.T) {
	c := testCoder(t)
	for _, name := range []string{AgentsFileName, AgentsLocalFileName} {
		if err := os.WriteFile(filepath.Join(c.Root, name), []byte("# rules\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c.AddFile(filepath.Join(c.Root, AgentsFileName))
	if note := c.pinnedFilesNote(); strings.Contains(note, AgentsLocalFileName) {
		t.Errorf("the private file is named while unpinned:\n%s", note)
	}
	c.AddFile(filepath.Join(c.Root, AgentsLocalFileName))
	note := c.pinnedFilesNote()
	if !strings.Contains(note, AgentsLocalFileName+" holds the user's private instructions") ||
		!strings.Contains(note, "it wins") {
		t.Errorf("the private file's clause is missing:\n%s", note)
	}
}

// dirtyRepo is a Repo rooted somewhere, reporting some paths as dirty.
type dirtyRepo struct {
	fakeRepo

	root  string
	dirty map[string]bool
}

func (r *dirtyRepo) Root() string            { return r.root }
func (r *dirtyRepo) IsDirty(rel string) bool { return r.dirty[rel] }

// An untracked AGENTS.local.md never joins a commit; staging it, ignored,
// would make git refuse the rest of the commit too. One the user tracks on
// purpose shows as dirty when edited, and is committed like any file.
func TestAgentsLocalStaysOutOfCommits(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	repo := &dirtyRepo{root: c.Root, dirty: map[string]bool{}}
	c.Repo = repo

	got := c.committablePaths([]string{"a.go", AgentsLocalFileName})
	if len(got) != 1 || got[0] != "a.go" {
		t.Errorf("committable = %v, want [a.go]", got)
	}

	repo.dirty[AgentsLocalFileName] = true
	if got := c.committablePaths([]string{"a.go", AgentsLocalFileName}); len(got) != 2 {
		t.Errorf("a tracked AGENTS.local.md was dropped: %v", got)
	}
}

// ignoringRepo reports every path as ignored, the state AGENTS.local.md is in
// once Strument has excluded it.
type ignoringRepo struct{ dirtyRepo }

func (r *ignoringRepo) GitIgnored(string) bool { return true }

// Ignored so that it stays private, not because it is out of scope, so the
// edit tools still write it; other ignored files stay refused.
func TestAgentsLocalIsEditableThoughIgnored(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	c.Repo = &ignoringRepo{dirtyRepo{root: c.Root}}
	if ok, why := c.allowedToEdit(AgentsLocalFileName, map[string]bool{}); !ok {
		t.Errorf("AGENTS.local.md was refused: %s", why)
	}
	if ok, _ := c.allowedToEdit("build/out.txt", map[string]bool{}); ok {
		t.Error("an ordinary ignored file must still be refused")
	}
}

package coder

import (
	"strings"
	"testing"
)

// dirtyRepo is a Repo rooted somewhere, reporting some paths as dirty.
type dirtyRepo struct {
	fakeRepo

	root  string
	dirty map[string]bool
}

func (r *dirtyRepo) Root() string                         { return r.root }
func (r *dirtyRepo) IsDirty(rel string) bool              { return r.dirty[rel] }
func (r *dirtyRepo) DirtyPaths() (map[string]bool, error) { return r.dirty, nil }

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

// Ignored so that it stays private, which is its purpose, so an edit to it
// carries no note; another ignored file's edit is told it will not be
// committed and may be generated.
func TestIgnoredEditNote(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	c.Repo = &ignoringRepo{dirtyRepo{root: c.Root}}
	if note := c.ignoredEditNote(AgentsLocalFileName); note != "" {
		t.Errorf("AGENTS.local.md drew a note: %s", note)
	}
	if note := c.ignoredEditNote("build/out.txt"); !strings.Contains(note, "will not be committed") ||
		!strings.Contains(note, "generated") {
		t.Errorf("an ignored file's note = %q", note)
	}
}

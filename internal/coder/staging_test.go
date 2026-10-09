package coder

import (
	"fmt"
	"maps"
	"strings"
	"testing"
)

// fakeStagingRepo is a Repo whose index the test changes between listings, as a
// model's shell command would, and which records what each commit was given.
type fakeStagingRepo struct {
	committingRepo

	index  map[string]string
	head   map[string]string // HEAD's entries, for StagedChanges
	dirty  map[string]bool
	staged [][]string
	extras [][]string

	marks   map[string]map[string]string
	dropped []string
}

func (r *fakeStagingRepo) MarkIndex() (string, error) {
	if r.marks == nil {
		r.marks = map[string]map[string]string{}
	}
	id := fmt.Sprintf("mark-%d", len(r.marks))
	r.marks[id] = maps.Clone(r.index)
	return id, nil
}

func (r *fakeStagingRepo) IndexChangedSince(mark string) ([]string, error) {
	old := r.marks[mark]
	var changed []string
	for p, e := range r.index {
		if old[p] != e {
			changed = append(changed, p)
		}
	}
	for p := range old {
		if _, ok := r.index[p]; !ok {
			changed = append(changed, p)
		}
	}
	return changed, nil
}

func (r *fakeStagingRepo) DropMark(mark string) { r.dropped = append(r.dropped, mark) }

func (r *fakeStagingRepo) ConflictedPaths(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if strings.Contains(r.index[p], " 1;") || strings.Contains(r.index[p], " 2;") {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakeStagingRepo) StagedChanges(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if r.index[p] != r.head[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakeStagingRepo) DirtyPaths() (map[string]bool, error) { return r.dirty, nil }

func (r *fakeStagingRepo) Commit(fnames, staged []string, context, message string, attributed bool, extra []string) (string, string, bool, error) {
	r.staged = append(r.staged, staged)
	r.extras = append(r.extras, extra)
	return r.committingRepo.Commit(fnames, staged, context, message, attributed, extra)
}

// A model command's staging reaches the turn's commit: both halves of a
// rename, but not a path that was already staged before the command, and not
// a conflicted one, which is named instead.
func TestStagedByCommandIsCommitted(t *testing.T) {
	c := testCoder(t)
	out := &captureOut{}
	c.Out = out
	repo := &fakeStagingRepo{
		index: map[string]string{
			"a.txt":    "100644 aaa 0;",
			"user.txt": "100644 uuu 0;",
			"c.txt":    "100644 ccc 0;",
		},
		head: map[string]string{
			"a.txt":    "100644 aaa 0;",
			"user.txt": "100644 u0 0;",
			"c.txt":    "100644 ccc 0;",
		},
		dirty: map[string]bool{"a.txt": true, "user.txt": true},
	}
	c.Repo = repo
	c.AutoCommits = true
	c.initBeforeMessage()

	snap := c.markIndex()
	// git mv a.txt b.txt; and a merge that left c.txt conflicted.
	delete(repo.index, "a.txt")
	repo.index["b.txt"] = "100644 aaa 0;"
	repo.index["c.txt"] = "100644 c1 1;100644 c2 2;100644 c3 3;"
	c.noteStaged(snap)

	tc, err := c.commitTurn("refactor: rename a")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(repo.staged[0], ","); got != "a.txt,b.txt" {
		t.Errorf("staged = %q, want both halves of the rename", got)
	}
	if got := strings.Join(tc.conflicted, ","); got != "c.txt" {
		t.Errorf("conflicted = %q", got)
	}
	// a.txt was dirty at turn start; user.txt was too but is not committed.
	if got := strings.Join(repo.extras[0], "|"); got != "Uncommitted-before-edit: a.txt" {
		t.Errorf("trailers = %q", got)
	}
	screen := strings.Join(out.lines, "\n")
	for _, want := range []string{
		"Not committing c.txt: unresolved merge conflicts.",
		"Included what the model's commands staged with git: a.txt, b.txt.",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	if c.turnStaged["a.txt"] || c.turnStaged["b.txt"] || !c.turnStaged["c.txt"] {
		t.Errorf("turnStaged after commit = %v, want only c.txt left", c.turnStaged)
	}
}

// With auto-commits off nothing is recorded at all: there is no commit for the
// record to feed.
func TestStagingUntrackedWhenCommitsOff(t *testing.T) {
	c := testCoder(t)
	repo := &fakeStagingRepo{index: map[string]string{"a.txt": "x"}}
	c.Repo = repo
	c.AutoCommits = false
	c.initBeforeMessage()
	if c.markIndex().ok || c.startMark.ok || c.dirtyAtStart != nil || len(repo.marks) != 0 {
		t.Error("tracked staging with auto-commits off")
	}
}

// The commit tool commits staging alone — a `git mv` with no edit after it —
// and tells the model what went in.
func TestCommitToolCommitsStagingAlone(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	repo := &fakeStagingRepo{
		index: map[string]string{"a.txt": "100644 aaa 0;"},
		head:  map[string]string{"a.txt": "100644 aaa 0;"},
	}
	c.Repo = repo
	c.AutoCommits = true
	c.initBeforeMessage()
	snap := c.markIndex()
	delete(repo.index, "a.txt")
	repo.index["b.txt"] = "100644 aaa 0;"
	c.noteStaged(snap)

	got := c.runCommitTool(commitArgs{subject: "refactor: rename a"})
	want := "Committed abc1234: refactor: rename a\nIt includes what your shell commands staged with git: a.txt, b.txt."
	if got != want {
		t.Errorf("result = %q\nwant     %q", got, want)
	}
}

// Live findings: a command that unstages the user's work (Qwen's
// `git reset --mixed`) or unstages it and stages it back (MiMo's
// `git restore --staged` then `git add`) touched the index without staging a
// change of the model's. Neither path is committed or announced.
func TestUnstagingIsNotStaging(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  string
	}{
		{"reset to HEAD", "100644 u0 0;"},
		{"restored as the user left it", "100644 u1 0;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCoder(t)
			out := &captureOut{}
			c.Out = out
			repo := &fakeStagingRepo{
				index: map[string]string{"user.txt": "100644 u1 0;"}, // the user's staged work
				head:  map[string]string{"user.txt": "100644 u0 0;"},
				dirty: map[string]bool{"user.txt": true},
			}
			c.Repo = repo
			c.AutoCommits = true
			c.initBeforeMessage()
			snap := c.markIndex()
			repo.index["user.txt"] = "100644 u0 0;" // unstaged
			c.noteStaged(snap)
			snap = c.markIndex()
			repo.index["user.txt"] = tc.now
			c.noteStaged(snap)

			if _, err := c.commitTurn("chore: nothing"); err != nil {
				t.Fatal(err)
			}
			if len(repo.staged) != 0 {
				t.Errorf("Commit called with staged %v, want no commit", repo.staged)
			}
			if c.turnStaged["user.txt"] {
				t.Error("user.txt still recorded as staged")
			}
		})
	}
}

// Live finding (GLM, Qwen): a path the model staged and then edited went into
// the first commit as an edit, and the second commit announced it again.
func TestStagedThenEditedIsAnnouncedOnce(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	repo := &fakeStagingRepo{
		index: map[string]string{"a.txt": "100644 aaa 0;"},
		head:  map[string]string{"a.txt": "100644 aaa 0;"},
	}
	c.Repo = repo
	c.AutoCommits = true
	c.initBeforeMessage()
	snap := c.markIndex()
	delete(repo.index, "a.txt")
	repo.index["b.txt"] = "100644 aaa 0;"
	c.noteStaged(snap)
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("b.txt", snapEntry{before: []byte("aaa\n"), existed: true}, "aaa\nmore\n")

	if _, err := c.commitTurn("first"); err != nil {
		t.Fatal(err)
	}
	if c.turnStaged["b.txt"] || c.turnStaged["a.txt"] {
		t.Errorf("turnStaged after the first commit = %v, want empty", c.turnStaged)
	}
}

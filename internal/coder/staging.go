package coder

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// What a turn's commit takes beyond the turn's own edits, what it leaves to a
// commit of its own, and how it knows.
//
// What the model's shell commands staged goes into the commit, each path as
// it is on disk (gitrepo's Commit says why disk and not the staged copy). A
// session showed why it goes in at all: MiMo ran `git mv test.ts test.mjs`,
// then edited test.mjs, and the commit took test.mjs alone, leaving the
// staged deletion of test.ts behind — half a rename. Rename detection cannot
// repair that reliably: git stores no renames, and pairing a deletion with an
// addition by similarity misses a rename whose target was rewritten and
// pairs a deletion the user staged with an unrelated new file of the model's.
// So instead the index is recorded before and after every model-caused shell
// command, and whatever changed in between is the model's, whatever command
// did it. /run never comes through here, so what the user stages stays
// theirs.
//
// Uncommitted changes a file had before the model first touched it are
// committed on their own, ahead of the model's commit, as aider does. Strument
// briefly put them in the model's commit instead, named in a trailer, and a
// trial (doc/experiments/2026-10-commit-whole-file) showed what that costs:
// in 5 of 96 runs a model deleted the user's uncommitted line to keep it out
// of its commit, four of them right after being told the line was in. With
// the user's changes already committed there is nothing to keep out.
//
// What made the old version of this commit wrong is what this one is built
// around. It fired at the first edit and took the file from disk, so a
// model's `git mv` or `sed -i` before the edit was committed as the user's
// work — half a rename, under a side model's message. Now the baseline is
// taken before the model's work begins, contents included, and the commit is
// of those contents, so nothing the model did can be in it; its message is
// fixed; and it needs commits on and no dry run, like any other commit.
//
// "Before the model's work begins" is the start of the first turn after the
// model's last commit, not the start of every turn. While each turn ended in
// a commit the two were the same. Now a turn can leave its edits for a later
// turn to commit, and a baseline retaken in that later turn would read the
// model's own uncommitted edits as the user's. So the record — edits, staged
// paths, index mark, baseline — runs from one commit to the next, across as
// many turns as it takes, and only the commit closes it.
//
// It also commits the baseline only when the model commits. It used to commit
// it at the model's first edit to the file, which was harmless while that
// edit was certain to be committed at turn end; without that it would commit
// the user's work for a model that never commits.

// stagingRepo is the optional part of a Repo that can record the index, list
// the dirty paths, and commit saved contents. gitrepo implements it; without
// it, a turn's commit is its edits alone.
type stagingRepo interface {
	// DirtyPaths is IsDirty for every tracked path at once.
	DirtyPaths() (map[string]bool, error)
	// MarkIndex records the index as it is now and returns the record, ""
	// for no index; IndexChangedSince lists the paths whose entries differ
	// from a record's; DropMark deletes one.
	MarkIndex() (string, error)
	IndexChangedSince(mark string) ([]string, error)
	DropMark(mark string)
	// StagedChanges lists which of paths the index holds differently from
	// HEAD, and ConflictedPaths which have unresolved merge conflicts.
	StagedChanges(paths []string) ([]string, error)
	ConflictedPaths(paths []string) ([]string, error)
	// CommitContents commits these contents, nil for a deletion, and nothing
	// else, unattributed.
	CommitContents(files map[string][]byte, message string) (hash string, ok bool, err error)
}

// indexMark is a MarkIndex record; ok is false when none was taken.
type indexMark struct {
	path string
	ok   bool
}

// staging returns the Repo's staging part when there is a commit for it to
// feed, or nil.
func (c *Coder) staging() stagingRepo {
	if c.Repo == nil || !c.Commits || c.DryRun {
		return nil
	}
	s, _ := c.Repo.(stagingRepo)
	return s
}

// takeStagingBaseline starts the record of the model's work since its last
// commit, unless one is already open: what was already uncommitted and its
// contents, the index as the turn found it, and nothing pending yet. Called at
// turn start.
func (c *Coder) takeStagingBaseline() {
	if c.pendingOpen {
		return
	}
	c.dropStartMark()
	c.userBaseline, c.pendingStaged, c.pendingEdits = nil, nil, nil
	c.pendingOpen = true
	s := c.staging()
	if s == nil {
		return
	}
	c.startMark = c.markIndex()
	dirty, err := s.DirtyPaths()
	if err != nil {
		return
	}
	c.userBaseline = c.readUncommitted(slices.Sorted(maps.Keys(dirty)))
}

// Limits on what the baseline reads at turn start. A file past them is not
// committed separately: its uncommitted changes go into the model's commit
// with the model's, as they would with no baseline at all. A tree with
// hundreds of dirty files is mid-checkout or mid-rewrite, not someone's
// work in progress, and reading it every turn would cost more than the
// separation is worth.
const (
	maxUncommittedFiles = 200
	maxUncommittedFile  = 4 << 20
	maxUncommittedTotal = 16 << 20
)

// readUncommitted reads the turn-start contents of the dirty paths, within
// the limits. A path gone from disk is the user's deletion, recorded as nil.
// A symlink is skipped: its contents are a target, not bytes to commit.
func (c *Coder) readUncommitted(paths []string) map[string][]byte {
	if len(paths) == 0 || len(paths) > maxUncommittedFiles {
		return nil
	}
	root := c.Repo.Root()
	if root == "" {
		return nil
	}
	saved := map[string][]byte{}
	total := 0
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		st, err := os.Lstat(full)
		switch {
		case errors.Is(err, os.ErrNotExist):
			saved[p] = nil
			continue
		case err != nil, !st.Mode().IsRegular(), st.Size() > maxUncommittedFile:
			continue
		}
		if total += int(st.Size()); total > maxUncommittedTotal {
			break
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		if data == nil {
			data = []byte{}
		}
		saved[p] = data
	}
	return saved
}

// notePending adds the paths a settle closed to the model's uncommitted work.
func (c *Coder) notePending(paths []string) {
	for _, p := range paths {
		if c.pendingEdits == nil {
			c.pendingEdits = map[string]bool{}
		}
		c.pendingEdits[p] = true
	}
}

// closePending ends the record after a commit took everything in it, so the
// next turn starts a new one. The baseline is kept until then: a later edit
// in this turn still has its user's version recorded.
func (c *Coder) closePending() {
	if len(c.pendingEdits) == 0 && len(c.pendingStaged) == 0 {
		c.pendingOpen = false
	}
}

// commitUncommittedFirst commits, on its own, what any of paths had
// uncommitted before the model's work on them began, ahead of the model's
// commit. Each path is committed this way once a record. A
// failure — a hook refusing the commit, most often — is said and not
// retried: the changes then go in with the model's, which is where they
// would have been anyway.
func (c *Coder) commitUncommittedFirst(paths []string) string {
	s := c.staging()
	if s == nil || len(c.userBaseline) == 0 {
		return ""
	}
	files := map[string][]byte{}
	for _, p := range paths {
		if data, ok := c.userBaseline[p]; ok {
			files[p] = data
			delete(c.userBaseline, p)
		}
	}
	if len(files) == 0 {
		return ""
	}
	names := strings.Join(slices.Sorted(maps.Keys(files)), ", ")
	hash, ok, err := s.CommitContents(files, "Commit existing changes to "+names+" before Strument's edits")
	switch {
	case err != nil:
		c.Out.Errorf("Could not commit the existing changes to %s first, so they will be in the model's commit: %v", names, err)
	case ok:
		c.Out.Toolf("Committed existing changes to %s first: %s", names, hash)
		return hash
	}
	return ""
}

// dropStartMark deletes the turn's starting record of the index, once the
// turn has settled or the next one begins.
func (c *Coder) dropStartMark() {
	if c.startMark.ok {
		if s, ok := c.Repo.(stagingRepo); ok {
			s.DropMark(c.startMark.path)
		}
	}
	c.startMark = indexMark{}
}

// markIndex records the index before a model-caused shell command, or
// returns no mark when there is nothing to track.
func (c *Coder) markIndex() indexMark {
	s := c.staging()
	if s == nil {
		return indexMark{}
	}
	m, err := s.MarkIndex()
	if err != nil {
		return indexMark{}
	}
	return indexMark{path: m, ok: true}
}

// noteStaged records the paths whose index entries changed since the mark,
// and deletes the mark.
func (c *Coder) noteStaged(m indexMark) {
	s, _ := c.Repo.(stagingRepo)
	if !m.ok || s == nil {
		return
	}
	defer s.DropMark(m.path)
	changed, err := s.IndexChangedSince(m.path)
	if err != nil {
		return
	}
	for _, p := range changed {
		if c.pendingStaged == nil {
			c.pendingStaged = map[string]bool{}
		}
		c.pendingStaged[p] = true
	}
}

// stagedForCommit splits the turn's staged paths, less those already among
// edited, into what the commit can take and what it cannot: a path with
// unresolved merge conflicts has no single entry to commit.
//
// A command touching a path's index entry is not the same as the model
// staging a change to it, and two live runs showed the difference. Qwen ran
// `git reset --mixed HEAD~1`, which unstaged the user's staged work: the
// entry changed, back to HEAD's. MiMo unstaged the user's work to keep it out
// of a commit and then put it back with `git add`: the entry changed twice
// and ended where the user left it. Neither is a change of the model's to
// commit. So a path is taken only when its entry differs both from where the
// turn found it and from HEAD; the rest are dropped from the record.
func (c *Coder) stagedForCommit(edited []string) (staged, conflicted []string) {
	s := c.staging()
	if len(c.pendingStaged) == 0 || s == nil {
		return nil, nil
	}
	var sinceStart []string
	if c.startMark.ok {
		var err error
		if sinceStart, err = s.IndexChangedSince(c.startMark.path); err != nil {
			return nil, nil
		}
	}
	var cands []string
	for _, p := range c.dropAgentsLocal(slices.Sorted(maps.Keys(c.pendingStaged))) {
		switch {
		case slices.Contains(edited, p):
			// Committed from the working tree as an edit.
		case c.startMark.ok && !slices.Contains(sinceStart, p):
			delete(c.pendingStaged, p)
		default:
			cands = append(cands, p)
		}
	}
	conflicted, err := s.ConflictedPaths(cands)
	if err != nil {
		return nil, nil
	}
	cands = slices.DeleteFunc(cands, func(p string) bool { return slices.Contains(conflicted, p) })
	changed, err := s.StagedChanges(cands)
	if err != nil {
		return nil, conflicted
	}
	for _, p := range cands {
		if slices.Contains(changed, p) {
			staged = append(staged, p)
		} else {
			delete(c.pendingStaged, p)
		}
	}
	return staged, conflicted
}

// conflictedNote tells the model which staged paths were left out.
func conflictedNote(paths []string) string {
	return fmt.Sprintf("Not committed, because of unresolved merge conflicts: %s.", strings.Join(paths, ", "))
}

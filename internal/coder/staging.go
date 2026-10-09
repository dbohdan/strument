package coder

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// What a turn's commit takes beyond the turn's own edits, and how it knows.
//
// Two things, both decided by provenance rather than by a guess.
//
// What the model's shell commands staged goes into the commit. A session
// showed why: MiMo ran `git mv test.ts test.mjs`, then edited test.mjs, and
// the commit took test.mjs alone, leaving the staged deletion of test.ts
// behind — half a rename. Rename detection cannot repair that reliably: git
// stores no renames, and pairing a deletion with an addition by similarity
// misses a rename whose target was rewritten and pairs a deletion the user
// staged with an unrelated new file of the model's. So instead the index is
// listed before and after every model-caused shell command, and whatever
// changed in between is the model's, whatever command did it. /run never
// comes through here, so what the user stages stays theirs.
//
// Paths that had uncommitted changes when the turn began are committed with
// it and named in an Uncommitted-before-edit trailer. This replaced a commit:
// aider committed such a file on its own before editing it ("dirty commits"),
// and Strument did too until the session above, where the dirty commit was
// what took test.mjs alone, under a side model's message, as though the user
// had made it. It also fired with auto-commits off and in a dry run, both of
// which promise no commits. Leaving such files out of the commit instead was
// considered and rejected: a file the model keeps editing would stay
// uncommitted turn after turn. The baseline is taken once, at turn start, so
// that a change the model makes before its first edit — a `sed -i`, a
// `git mv` — is not mistaken for one that was already there.

// stagingRepo is the optional part of a Repo that can record the index and
// list the dirty paths. gitrepo implements it; without it, a turn's commit is
// its edits alone, with no trailers.
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
}

// indexMark is a MarkIndex record; ok is false when none was taken.
type indexMark struct {
	path string
	ok   bool
}

// staging returns the Repo's staging part when there is a commit for it to
// feed, or nil.
func (c *Coder) staging() stagingRepo {
	if c.Repo == nil || !c.AutoCommits || c.DryRun {
		return nil
	}
	s, _ := c.Repo.(stagingRepo)
	return s
}

// takeStagingBaseline starts a turn's record: what was already uncommitted,
// the index as the turn found it, and nothing staged yet.
func (c *Coder) takeStagingBaseline() {
	c.dropStartMark()
	c.dirtyAtStart, c.turnStaged = nil, nil
	s := c.staging()
	if s == nil {
		return
	}
	// On error, no trailers: the commit is still right, only less annotated.
	c.dirtyAtStart, _ = s.DirtyPaths()
	c.startMark = c.markIndex()
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
		if c.turnStaged == nil {
			c.turnStaged = map[string]bool{}
		}
		c.turnStaged[p] = true
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
	if len(c.turnStaged) == 0 || s == nil {
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
	for _, p := range c.dropAgentsLocal(slices.Sorted(maps.Keys(c.turnStaged))) {
		switch {
		case slices.Contains(edited, p):
			// Committed from the working tree as an edit.
		case c.startMark.ok && !slices.Contains(sinceStart, p):
			delete(c.turnStaged, p)
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
			delete(c.turnStaged, p)
		}
	}
	return staged, conflicted
}

// conflictedNote tells the model which staged paths were left out.
func conflictedNote(paths []string) string {
	return fmt.Sprintf("Not committed, because of unresolved merge conflicts: %s.", strings.Join(paths, ", "))
}

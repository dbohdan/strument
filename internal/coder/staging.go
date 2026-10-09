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

// stagingRepo is the optional part of a Repo that can list the index and the
// dirty paths. gitrepo implements it; without it, a turn's commit is its edits
// alone, with no trailers.
type stagingRepo interface {
	// DirtyPaths is IsDirty for every tracked path at once.
	DirtyPaths() (map[string]bool, error)
	// IndexEntries lists the index as path → its entries, so that two
	// listings differ exactly where something was staged in between.
	IndexEntries() (map[string]string, error)
	// StagedChanges lists which of paths the index holds differently from
	// HEAD.
	StagedChanges(paths []string) ([]string, error)
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
// and nothing staged yet.
func (c *Coder) takeStagingBaseline() {
	c.dirtyAtStart, c.turnStaged, c.indexAtStart = nil, nil, nil
	s := c.staging()
	if s == nil {
		return
	}
	// On error, no trailers: the commit is still right, only less annotated.
	c.dirtyAtStart, _ = s.DirtyPaths()
	c.indexAtStart, _ = s.IndexEntries()
}

// indexSnapshot lists the index before a model-caused shell command, or nil
// when there is nothing to track.
func (c *Coder) indexSnapshot() map[string]string {
	s := c.staging()
	if s == nil {
		return nil
	}
	idx, err := s.IndexEntries()
	if err != nil {
		return nil
	}
	return idx
}

// noteStaged records the paths whose index entries changed since before.
func (c *Coder) noteStaged(before map[string]string) {
	s := c.staging()
	if before == nil || s == nil {
		return
	}
	after, err := s.IndexEntries()
	if err != nil {
		return
	}
	mark := func(p string) {
		if c.turnStaged == nil {
			c.turnStaged = map[string]bool{}
		}
		c.turnStaged[p] = true
	}
	for p, e := range after {
		if before[p] != e {
			mark(p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			mark(p)
		}
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
	idx, err := s.IndexEntries()
	if err != nil {
		return nil, nil
	}
	var cands []string
	for _, p := range c.dropAgentsLocal(slices.Sorted(maps.Keys(c.turnStaged))) {
		switch {
		case slices.Contains(edited, p):
			// Committed from the working tree as an edit.
		case c.indexAtStart != nil && idx[p] == c.indexAtStart[p]:
			delete(c.turnStaged, p)
		case conflictedEntry(idx[p]):
			conflicted = append(conflicted, p)
		default:
			cands = append(cands, p)
		}
	}
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

// conflictedEntry reports whether an IndexEntries value holds a stage other
// than 0. Each entry is "mode blob stage;".
func conflictedEntry(e string) bool {
	for f := range strings.SplitSeq(e, ";") {
		if fs := strings.Fields(f); len(fs) == 3 && fs[2] != "0" {
			return true
		}
	}
	return false
}

// conflictedNote tells the model which staged paths were left out.
func conflictedNote(paths []string) string {
	return fmt.Sprintf("Not committed, because of unresolved merge conflicts: %s.", strings.Join(paths, ", "))
}

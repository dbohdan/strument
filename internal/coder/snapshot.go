package coder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// errNothingToUndo is what /undo reports when this session has changed nothing.
var errNothingToUndo = errors.New("no Strument edits to undo in this session")

// A turn's edits are recoverable whether or not there is a repository. Git
// records them as a commit; the snapshot here records them as bytes, which is
// what makes Strument usable on a live configuration directory or under another
// SCM. The two are kept in step: an undo pops the stack either way.
//
// The contents come free. writeAtomically already reads every file it is about
// to write so it can roll a failed batch back; a snapshot is that same reading,
// kept instead of discarded once the batch succeeds.

// snapEntry is one file's before and after within a turn. before is the state
// at the turn's *first* write — a later batch touching the same file must not
// overwrite it, or an undo would only unwind the last step. after is the
// content of the most recent write, which is what tells an undo whether
// something else has changed the file since.
type snapEntry struct {
	before  []byte
	existed bool
	after   []byte
}

// turnSnapshot is what one turn did to the working tree, in first-touch order.
type turnSnapshot struct {
	order   []string
	entries map[string]*snapEntry
}

func newTurnSnapshot() *turnSnapshot {
	return &turnSnapshot{entries: map[string]*snapEntry{}}
}

// record notes one write. The first call for a path fixes its before-state;
// every call updates the after-state.
func (s *turnSnapshot) record(path string, before []byte, existed bool, after string) {
	e, ok := s.entries[path]
	if !ok {
		e = &snapEntry{before: before, existed: existed}
		s.entries[path] = e
		s.order = append(s.order, path)
	}
	e.after = []byte(after)
}

func (s *turnSnapshot) empty() bool { return s == nil || len(s.order) == 0 }

// recordWrites folds a completed batch into the current turn's snapshot. It is
// called only after the whole batch landed: a batch that rolled back changed
// nothing and must leave no trace here.
func (c *Coder) recordWrites(plan writePlan, before map[string]snapEntry) {
	if c.turnSnap == nil {
		c.turnSnap = newTurnSnapshot()
	}
	for _, rel := range plan.WriteOrder {
		b := before[rel]
		c.turnSnap.record(rel, b.before, b.existed, plan.Writes[rel])
	}
}

// pushTurnSnapshot closes the turn's snapshot and puts it on the undo stack.
// An empty turn leaves the stack alone, so /undo always reaches the last turn
// that actually changed something.
func (c *Coder) pushTurnSnapshot() {
	if c.turnSnap.empty() {
		c.turnSnap = nil
		return
	}
	c.undoStack = append(c.undoStack, c.turnSnap)
	c.turnSnap = nil
}

// HasTurnSnapshot reports whether there is a turn to undo.
func (c *Coder) HasTurnSnapshot() bool { return len(c.undoStack) > 0 }

// DropTurnSnapshot discards the most recent turn's snapshot without restoring
// anything — what the git path calls after undoing through the repository, so
// the two records of the same turn stay in step.
func (c *Coder) DropTurnSnapshot() {
	if n := len(c.undoStack); n > 0 {
		c.undoStack = c.undoStack[:n-1]
	}
}

// SquashTurns collapses the last n turns into one on the undo stack and records
// hash as a session commit, so /undo after a /squash unwinds the same range the
// squash commit covers. Earliest before-state per path wins; latest after-state
// wins — exactly what one long turn would have recorded.
func (c *Coder) SquashTurns(hash string, n int) {
	if hash != "" {
		if c.sessionCommits == nil {
			c.sessionCommits = map[string]bool{}
		}
		c.sessionCommits[hash] = true
		c.lastCommitHash = hash
	}

	if n < 2 || len(c.undoStack) < n {
		return
	}
	head := len(c.undoStack) - n
	merged := newTurnSnapshot()
	for _, snap := range c.undoStack[head:] {
		for _, rel := range snap.order {
			e := snap.entries[rel]
			merged.record(rel, e.before, e.existed, string(e.after))
		}
	}
	c.undoStack = append(c.undoStack[:head], merged)
}

// UndoLastTurn puts back what the last turn wrote and returns the paths it
// restored. It is the no-git undo; with a repository the commit is the record
// and /undo moves HEAD instead.
//
// Nothing is restored unless everything can be: a file whose contents no longer
// match what Strument wrote has been changed by someone else, and quietly
// overwriting that is the one outcome an undo must never produce. This is the
// same judgement as the git path's refusal to undo over uncommitted changes.
func (c *Coder) UndoLastTurn() ([]string, error) {
	n := len(c.undoStack)
	if n == 0 {
		return nil, errNothingToUndo
	}
	snap := c.undoStack[n-1]

	for _, rel := range snap.order {
		e := snap.entries[rel]
		current, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(rel)))
		if err != nil {
			// Gone from disk. Whatever happened to it, putting the previous
			// contents back cannot destroy work that is no longer there.
			continue
		}
		if string(current) != string(e.after) {
			return nil, fmt.Errorf("%s has changed since Strument wrote it; undo would discard that", rel)
		}
	}

	var restored []string
	for _, rel := range snap.order {
		e := snap.entries[rel]
		full := filepath.Join(c.Root, filepath.FromSlash(rel))
		if !e.existed {
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				return restored, fmt.Errorf("could not remove %s: %w", rel, err)
			}
			restored = append(restored, rel)
			continue
		}
		if err := os.WriteFile(full, e.before, 0o644); err != nil { //nolint:gosec // Restoring a project file; sources are world-readable.
			return restored, fmt.Errorf("could not restore %s: %w", rel, err)
		}
		restored = append(restored, rel)
	}

	c.undoStack = c.undoStack[:n-1]
	return restored, nil
}

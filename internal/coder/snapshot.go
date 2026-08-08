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
	mode    os.FileMode
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
func (s *turnSnapshot) record(path string, before snapEntry, after string) {
	e, ok := s.entries[path]
	if !ok {
		e = &snapEntry{before: before.before, existed: before.existed, mode: before.mode}
		s.entries[path] = e
		s.order = append(s.order, path)
	}
	e.after = []byte(after)
}

func (s *turnSnapshot) empty() bool { return s == nil || len(s.order) == 0 }

// wrote reports whether this turn has already written path.
func (s *turnSnapshot) wrote(path string) bool {
	if s == nil {
		return false
	}
	_, ok := s.entries[path]
	return ok
}

// recordWrites folds a completed batch into the current turn's snapshot. It is
// called only after the whole batch landed: a batch that rolled back changed
// nothing and must leave no trace here.
func (c *Coder) recordWrites(plan writePlan, before map[string]snapEntry) {
	if c.turnSnap == nil {
		c.turnSnap = newTurnSnapshot()
	}
	for _, rel := range plan.WriteOrder {
		c.turnSnap.record(rel, before[rel], plan.Writes[rel])
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
			merged.record(rel, *e, string(e.after))
		}
	}
	c.undoStack = append(c.undoStack[:head], merged)
}

// UndoLastTurn puts back what the last turn wrote and returns the paths it
// restored. It is the no-git undo; with a repository the commit is the record
// and /undo moves HEAD instead.
//
// Nothing is restored unless everything is: a file whose contents no longer
// match what Strument wrote has been changed by someone else, and quietly
// overwriting that is the one outcome an undo must never produce. This is the
// same judgement as the git path's refusal to undo over uncommitted changes.
// A file the undo cannot write — read-only, or a directory now standing where
// the file was — is the same problem arriving later, so a failure part of the
// way through puts the turn's own result back rather than leaving the tree in a
// state neither the user nor Strument asked for.
//
// It restores contents and not modes, deliberately. writeAtomically preserves a
// file's mode through an edit, so the turn never changed one; a chmod between
// the turn and the undo belongs to whoever ran it.
func (c *Coder) UndoLastTurn() ([]string, error) {
	n := len(c.undoStack)
	if n == 0 {
		return nil, errNothingToUndo
	}
	snap := c.undoStack[n-1]

	for _, rel := range snap.order {
		e := snap.entries[rel]
		full := c.fullPath(rel)
		current, err := os.ReadFile(full)
		if err != nil {
			// Gone from disk. Whatever happened to it, putting the previous
			// contents back cannot destroy work that is no longer there.
			continue
		}
		if string(current) != string(e.after) {
			return nil, fmt.Errorf("%s has changed since Strument wrote it; undo would discard that", rel)
		}
		if e.existed {
			// Fail before touching anything rather than halfway through. The
			// rollback below is the backstop, not the plan.
			f, err := os.OpenFile(full, os.O_WRONLY, 0)
			if err != nil {
				return nil, fmt.Errorf("cannot write %s to undo it: %w", rel, err)
			}
			f.Close()
		}
	}

	var restored []string
	for _, rel := range snap.order {
		if err := c.restoreFile(rel, snap.entries[rel]); err != nil {
			c.redoFiles(snap, restored)
			return nil, fmt.Errorf("could not restore %s, so nothing was undone: %w", rel, err)
		}
		restored = append(restored, rel)
	}

	c.undoStack = c.undoStack[:n-1]
	return restored, nil
}

// restoreFile puts one file back the way the turn found it.
func (c *Coder) restoreFile(rel string, e *snapEntry) error {
	full := c.fullPath(rel)
	if !e.existed {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	// The mode argument applies only if the file has to be created — if it is
	// still there, its current mode is kept, which is the point.
	return os.WriteFile(full, e.before, e.mode)
}

// redoFiles puts the turn's own result back on the files an aborted undo had
// already reverted, so a failure leaves the tree where the undo found it.
func (c *Coder) redoFiles(snap *turnSnapshot, paths []string) {
	for _, rel := range paths {
		e := snap.entries[rel]
		full := c.fullPath(rel)
		mode := e.mode
		if !e.existed {
			mode = newFileMode // the turn created it; recreate it as the turn did
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
		}
		_ = os.WriteFile(full, e.after, mode)
	}
}

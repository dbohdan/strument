package coder

import (
	"bytes"
	"os"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// A turn stopped from outside — the REPL's second Ctrl-C, a closed terminal,
// `timeout` — used to end with the process and take its settle with it: no
// commit, no /undo, no turn record. The edits stayed on disk, and the next
// turn's commit took only the files the next turn touched. A Larkspur run
// committed tests without the code they needed that way, and the branch did not
// build.
//
// The fix is split by cost. On the way out, Strument does the local half of a
// settle, which a measurement put at 0.2 s for a hundred edits and under a
// second for a hundred 100 KiB files: the turn goes onto the undo stack, saved
// to disk, marked as not committed. The commit waits for the next start,
// because a commit runs the repository's hooks and asks the side model for a
// message, and neither belongs between a person and the exit they asked for.
//
// SIGKILL, a crash and power loss run nothing on the way out, and are not
// covered; the edits are still on disk, as before.

// exitWait bounds how long an exit waits for a write batch or a settle already
// under way. A batch takes milliseconds; a settle can be running a commit hook,
// and a person who asked to leave should not wait on one.
const exitWait = 2 * time.Second

// SaveOnExit puts the open turn's edits on the undo stack, saved and marked as
// not committed, and reports their paths. It stops the send first. It is for a
// process about to exit and leaves the Coder unfit for another turn.
func (c *Coder) SaveOnExit() []string {
	c.InterruptSend()
	deadline := time.Now().Add(exitWait)
	for !c.settleMu.TryLock() {
		// A commit under way will not finish: the process exits next. Its
		// snapshot is saved as uncommitted instead, and if the commit does
		// land first, the next start finds nothing left to commit. The first
		// version waited here for the lock, gave up, and exited, which killed
		// the commit after `git add` and saved nothing: a REPL's double Ctrl-C
		// does exactly that, its first press having started the settle.
		if s := c.getSettling(); s != nil {
			return c.saveSnapshotOnExit(s)
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The lock is released, but from here a batch refuses to start and a
	// settle does nothing: the process exits next, and an edit landing after
	// the snapshot was saved would be one the snapshot does not hold. Holding
	// the lock instead hung the REPL's tests, whose Exit returns.
	defer c.settleMu.Unlock()
	c.exiting = true
	if c.turnSnap.empty() {
		return nil
	}
	paths := c.turnSnap.paths()
	c.exitUncommitted = true
	c.pushTurnSnapshot()
	c.record(Record{Type: "turn", Call: "turn", Outcome: "exited", Time: c.Clock.Now().UTC().Format(time.RFC3339), Files: paths})
	return paths
}

// saveSnapshotOnExit saves s as the newest turn, uncommitted, without the
// settle lock, whose holder is mid-commit. The stack it reads is not changed
// until that commit returns, and the process exits right after this.
func (c *Coder) saveSnapshotOnExit(s *turnSnapshot) []string {
	c.exiting = true
	c.exitUncommitted = true
	if c.SaveUndo != nil {
		stack := c.UndoStack()
		turn := make([]TurnEdit, 0, len(s.order))
		for _, rel := range s.order {
			e := s.entries[rel]
			turn = append(turn, TurnEdit{Path: rel, Before: e.before, After: e.after, Existed: e.existed, Mode: e.mode})
		}
		hashes, last := c.SessionCommits()
		c.SaveUndo(append(stack, turn), hashes, last)
	}
	return s.paths()
}

// NewestTurnUncommitted reports whether the newest turn on the undo stack was
// saved on the way out and not committed, for the undo record to carry.
func (c *Coder) NewestTurnUncommitted() bool { return c.exitUncommitted }

// RecoverExited commits what an exited turn left, at the start of the next run.
// turn is that turn's undo entries. A file still byte-for-byte what the turn
// wrote is the turn's work and is committed, alone, before anything else
// happens; one that differs has been changed since, by a person or a program,
// and is only reported, so a hand edit never lands in a model-authored commit.
// The model is told once, in a harness note.
//
// With no repository, auto-commits off, or a dry run, nothing is committed and
// the same report is made: the turn is on the undo stack either way.
func (c *Coder) RecoverExited(turn []TurnEdit) (kept, changed []string, hash string, already bool, err error) {
	for _, e := range turn {
		// Unreadable or gone counts as changed: either way it is not what
		// the turn left.
		if cur, rerr := os.ReadFile(c.fullPath(e.Path)); rerr == nil && bytes.Equal(cur, e.After) {
			kept = append(kept, e.Path)
		} else {
			changed = append(changed, e.Path)
		}
	}
	if len(kept) > 0 && c.Repo != nil && c.AutoCommits && !c.DryRun {
		paths := c.committablePaths(kept)
		if len(paths) > 0 {
			h, _, ok, cerr := c.Repo.Commit(paths, c.commitContext(), "Commit the edits of a turn interrupted by exiting", true, nil)
			switch {
			case cerr != nil:
				err = cerr
			case !ok:
				// The interrupted commit landed before the exit did.
				already = true
			case ok:
				hash = h
				c.lastCommitHash = h
				if c.sessionCommits == nil {
					c.sessionCommits = map[string]bool{}
				}
				c.sessionCommits[h] = true
			}
		}
	}
	c.doneMessages = append(c.doneMessages, llm.HarnessNote(exitedNote(kept, changed, hash, already)))
	return kept, changed, hash, already, err
}

func exitedNote(kept, changed []string, hash string, already bool) string {
	s := "The last run ended in the middle of a turn, so that turn was never finished."
	if len(kept) > 0 {
		switch {
		case hash != "":
			s += " Its edits to " + joinPaths(kept) + " are committed as " + hash + "."
		case already:
			s += " Its edits to " + joinPaths(kept) + " were committed before it ended."
		default:
			s += " Its edits to " + joinPaths(kept) + " are in the files, uncommitted."
		}
	}
	if len(changed) > 0 {
		s += " " + ChangedSince(changed)
	}
	return s + " Check that turn's work before building on it."
}

func joinPaths(ps []string) string {
	switch len(ps) {
	case 1:
		return ps[0]
	case 2:
		return ps[0] + " and " + ps[1]
	}
	out := ""
	for i, p := range ps {
		switch {
		case i == len(ps)-1:
			out += ", and " + p
		case i > 0:
			out += ", " + p
		default:
			out = p
		}
	}
	return out
}

// ChangedSince is the sentence for files an exited turn wrote that have changed
// since, shared by the startup notice and the model's note.
func ChangedSince(paths []string) string {
	if len(paths) == 1 {
		return paths[0] + " changed after that run wrote it, and was left as it is."
	}
	return joinPaths(paths) + " changed after that run wrote them, and were left as they are."
}

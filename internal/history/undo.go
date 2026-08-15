package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// This file makes /undo survive a restart.
//
// The undo stack was in memory, which is fine where there is a repository —
// after a restart /undo refuses at the session-commit gate and `git reset --hard
// HEAD^` is right there. Without one it is data loss: a live configuration
// directory or a checkout under another SCM has no second record, so closing the
// terminal quietly ended the ability to undo the last turn, and nothing said so.
// That is exactly the workload the snapshot substrate exists for, so the gap sat
// in the one place the design was meant to cover.
//
// history.go already anticipated this: "the deferred undo spill is a subtree of
// copied source per session" is why a project gets a directory rather than a
// family of <key>.<ext> siblings, and why dirMode/fileMode are 0700/0600 —
// verbatim copies of source whose modes internal/coder goes to some trouble to
// preserve should not become world-readable one directory over.

// undoVersion is the schema version of undo.json.
const undoVersion = 1

// Retention. The stack is bounded from the bottom because only the top is
// reachable: /undo pops, so a turn buried under twenty others is not something
// anyone is about to restore, while its bytes are as heavy as the top one's.
//
// The byte cap is the one that matters. Entries hold a file's contents twice —
// before and after — so a turn that rewrites a large generated file costs twice
// its size, and a long session of those is how a state directory quietly becomes
// a gigabyte of somebody's source.
const (
	maxUndoTurns = 20
	maxUndoBytes = 8 << 20 // 8 MiB of before/after contents, oldest evicted first
)

// UndoEntry is one file's before and after within a turn. It mirrors coder's
// snapEntry, which is unexported and must stay that way — the coder owns the
// semantics, this package owns the bytes.
//
// Contents are []byte and marshal as base64, which is what makes this safe for a
// file that is not text. Mode is the file's mode at the turn's first write, kept
// because restoring a file the turn *created* has to create it as the turn did.
type UndoEntry struct {
	Path    string      `json:"path"`
	Before  []byte      `json:"before,omitempty"`
	After   []byte      `json:"after,omitempty"`
	Existed bool        `json:"existed"`
	Mode    os.FileMode `json:"mode"`
}

// UndoTurn is one turn's edits, in first-touch order — the order coder replays
// them in, which is why it is a slice and not a map.
type UndoTurn struct {
	Entries []UndoEntry `json:"entries"`
}

// UndoState is the persisted stack, oldest turn first, plus the git half.
//
// Commits are the short hashes of this project's Strument auto-commits. /undo
// with a repository gates on them (coder.IsSessionCommit) and the gate was
// session-scoped, so a restart made /undo refuse a commit it had made itself an
// hour earlier. Persisting them is safe because the gate is not the only one:
// the commit must still be HEAD, unpushed, single-parent, and its files clean.
// If a human committed on top, HEAD is not a Strument commit and the gate fails
// as it should.
type UndoState struct {
	Version int        `json:"version"`
	Updated string     `json:"updated"`
	Turns   []UndoTurn `json:"turns,omitempty"`
	Commits []string   `json:"commits,omitempty"`
	Last    string     `json:"last_commit,omitempty"`
}

// UndoPath is the undo file for a project root.
func UndoPath(projectRoot string) (string, error) {
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "undo.json"), nil
}

// LoadUndo reads a project's undo state. A missing, unreadable, malformed, or
// unrecognized-version file yields the zero value and no error — the same
// judgement as LoadResume, and for a stronger reason here: a stale stack cannot
// do damage. UndoLastTurn refuses any file whose contents no longer match what
// Strument wrote, so the worst a wrong stack produces is a refusal.
func LoadUndo(projectRoot string) UndoState {
	p, err := UndoPath(projectRoot)
	if err != nil {
		return UndoState{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return UndoState{}
	}
	var s UndoState
	if err := json.Unmarshal(data, &s); err != nil || s.Version != undoVersion {
		return UndoState{}
	}
	return s
}

// SaveUndo writes the undo state atomically, owner-only, after trimming it to
// the retention caps.
//
// The whole stack is rewritten rather than appended to. A content-addressed blob
// store would write less, and it would also need a garbage collector, a
// reference count, and a story for a half-written index — for a file that is
// single-digit megabytes and written once per turn. Rewriting is milliseconds
// and cannot leave a dangling reference.
func SaveUndo(projectRoot string, s UndoState) error {
	p, err := UndoPath(projectRoot)
	if err != nil {
		return err
	}
	s.Version = undoVersion
	s.Updated = time.Now().UTC().Format(time.RFC3339)
	s.Turns = trimUndoTurns(s.Turns)

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	// Created 0600 rather than chmod'd after: rename replaces the destination's
	// inode, so a mode set on the original does not survive. Same trap as
	// SaveResume, writeAtomically, and the vendored readline.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, fileMode); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// There is deliberately no ClearUndo. /reset and /clear forget the
// *conversation*; the edits they were about are still on disk, and taking away
// the ability to unwind them because the chat was cleared would be a regression
// wearing the clothes of tidiness. "Forget this project" is already an rm -rf of
// its state directory, which is the layout's whole point.

// trimUndoTurns drops the oldest turns until the stack fits both caps. It always
// keeps at least the newest turn: a single turn over the byte cap is still the
// one the user is about to undo, and refusing to record it would trade a bounded
// disk cost for unbounded surprise.
func trimUndoTurns(turns []UndoTurn) []UndoTurn {
	if len(turns) > maxUndoTurns {
		turns = turns[len(turns)-maxUndoTurns:]
	}
	total := 0
	for _, t := range turns {
		total += t.bytes()
	}
	for len(turns) > 1 && total > maxUndoBytes {
		total -= turns[0].bytes()
		turns = turns[1:]
	}
	return turns
}

func (t UndoTurn) bytes() int {
	n := 0
	for _, e := range t.Entries {
		n += len(e.Before) + len(e.After)
	}
	return n
}

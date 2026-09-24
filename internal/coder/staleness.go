package coder

import (
	"os"
	"sync"
	"time"
)

// Staleness detection: refuse an edit to a file that changed on disk since the
// model last saw it.
//
// Without this, a file the user saves in their own editor mid-turn either still
// matches the model's old_string — and the edit lands on content that has moved
// — or it does not, and the model is told "the search text was not found" with
// nothing to distinguish a typo it made from ground that shifted under it. The
// second is confusing; the first is a silent wrong write, which is the worse
// outcome a harness can produce.
//
// Only `read` records a stamp. Pinned file contents are re-read from disk at
// every send (assemble.go), so the model's copy of a pinned file cannot go
// stale between steps; a file it learned through the read tool can, because
// that result sits in the transcript unchanged for the rest of the turn.
//
// The check fails open by design. A file with no recorded stamp is editable as
// before, so nothing that works today stops working: the gate can only fire
// where the harness positively knows the file moved.

// fileStamp identifies a version of a file, and when it was noted relative to
// the commands this session has run. Modification time and size, the
// same pair repomap's tag cache keys on and the same heuristic git uses for its
// index. It misses a change that preserves both — a same-size rewrite inside
// one mtime tick — which is the direction to miss in: a missed change is
// today's behaviour, while a false alarm would block an edit that was fine.
type fileStamp struct {
	modTime time.Time
	size    int64
}

// notedStamp is a stamp plus the command count at the moment it was taken.
// The count does not decide whether the file moved; it decides how the
// refusal explains it.
type notedStamp struct {
	fileStamp

	commands int
}

// shownFiles records the version of each file the model was last shown.
type shownFiles struct {
	mu     sync.Mutex
	stamps map[string]notedStamp
	// commands counts the commands run through bash and the checks. A file
	// that moved after one of them may well have been moved by it — gofmt -w,
	// sed -i, a formatter behind a check — and the refusal used to tell the
	// model "the file was modified outside this conversation" when the model
	// had done it itself two steps earlier.
	commands int
}

func newShownFiles() *shownFiles {
	return &shownFiles{stamps: map[string]notedStamp{}}
}

func stampOf(full string) (fileStamp, bool) {
	fi, err := os.Stat(full)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{modTime: fi.ModTime(), size: fi.Size()}, true
}

// The methods below tolerate a nil receiver. A Coder assembled directly rather
// than through New has no record, and the answer for "was this file shown to
// the model" is then "nothing is known" — which is the same fail-open answer
// the rest of this file gives, rather than a panic on a path (the run_code
// bridge reaches read) that has nothing to do with staleness.

// note records the file's current version as the one the model has seen.
// Called after a read, and after the harness's own writes — a file Strument
// just wrote is a file the model's next edit may build on.
func (s *shownFiles) note(rel, full string) {
	if s == nil {
		return
	}
	stamp, ok := stampOf(full)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stamps[rel] = notedStamp{fileStamp: stamp, commands: s.commands}
}

// commandRan records that a command ran, one that could have rewritten any
// file the model has read.
func (s *shownFiles) commandRan() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands++
}

// changed reports whether rel has moved since it was noted, and whether a
// command has run since then. False when nothing was recorded, or when the
// file cannot be stat'd: both are "we do not know", and not knowing is not
// grounds for refusing an edit.
func (s *shownFiles) changed(rel, full string) (moved, afterCommand bool) {
	if s == nil {
		return false, false
	}
	s.mu.Lock()
	seen, ok := s.stamps[rel]
	commands := s.commands
	s.mu.Unlock()
	if !ok {
		return false, false
	}
	now, ok := stampOf(full)
	if !ok || now == seen.fileStamp {
		return false, false
	}
	return true, commands > seen.commands
}

// forget drops every stamp. Undo rewrites files behind the harness's back, so
// what the model was shown and what is on disk are both unknown afterwards;
// dropping the stamps returns to the fail-open state rather than reporting a
// staleness the user themselves caused.
func (s *shownFiles) forget() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.stamps)
}

// toolStaleFailure is the tool result for an edit refused because the file
// moved. It names the cause, because the model's next move depends on which
// one it is: a mismatch it can fix by looking harder, or a file it must read
// again because what it is holding is out of date.
//
// afterCommand says a command ran between the read and this edit. Then the
// cause is not known — the model's own command is as likely as the user's
// editor — and the result says so rather than asserting the one that sends a
// model looking for someone else in the room.
func toolStaleFailure(path string, afterCommand bool) string {
	head := "Nothing was changed: " + quoteToolArg(path) + " has changed on disk " +
		"since you read it, so the text you matched may have moved or be gone.\n"
	if afterCommand {
		return head + "Read it again before editing. A command run since you read it " +
			"may have changed it — a formatter, sed -i, a build step — or it was " +
			"edited outside this conversation.\n"
	}
	return head + "Read it again before editing. This is not a mistake in your edit — " +
		"the file was modified outside this conversation.\n"
}

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

// fileStamp identifies a version of a file. Modification time and size, the
// same pair repomap's tag cache keys on and the same heuristic git uses for its
// index. It misses a change that preserves both — a same-size rewrite inside
// one mtime tick — which is the direction to miss in: a missed change is
// today's behaviour, while a false alarm would block an edit that was fine.
type fileStamp struct {
	modTime time.Time
	size    int64
}

// shownFiles records the version of each file the model was last shown.
type shownFiles struct {
	mu     sync.Mutex
	stamps map[string]fileStamp
}

func newShownFiles() *shownFiles {
	return &shownFiles{stamps: map[string]fileStamp{}}
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
	s.stamps[rel] = stamp
}

// changed reports whether rel has moved since it was noted. False when nothing
// was recorded, or when the file cannot be stat'd: both are "we do not know",
// and not knowing is not grounds for refusing an edit.
func (s *shownFiles) changed(rel, full string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	seen, ok := s.stamps[rel]
	s.mu.Unlock()
	if !ok {
		return false
	}
	now, ok := stampOf(full)
	if !ok {
		return false
	}
	return now != seen
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
func toolStaleFailure(path string) string {
	return "Nothing was changed: " + quoteToolArg(path) + " has changed on disk " +
		"since you read it, so the text you matched may have moved or be gone.\n" +
		"Read it again before editing. This is not a mistake in your edit — " +
		"the file was modified outside this conversation.\n"
}

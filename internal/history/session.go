package history

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A project holds several conversations, each under its own name.
//
// The name is a directory name, which is the whole of why it is validated
// rather than taken as given. `--session ../../..` would otherwise reach out
// of the state directory, and `current` is a file a user can edit — so the
// name arrives from outside twice over.

// sessionNamePattern is what a session may be called: a letter or digit, then
// letters, digits, dots, dashes and underscores.
//
// Strict on purpose, and the leading character is the load-bearing part. It
// rules out "." and ".." without naming them, and with them every way a name
// reaches outside its own directory. What is left is what a person would type
// anyway — "review", "spike-2", "api_v2".
var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// maxSessionName is a length a filesystem will take on every platform Strument
// runs on, with room to spare. A name is a label, not a description.
const maxSessionName = 64

// ValidSessionName reports why a name cannot be used, or nil.
//
// The error is written to be shown to whoever typed the name, so it says what
// is allowed rather than which rule was broken.
func ValidSessionName(name string) error {
	switch {
	case name == "":
		return errors.New("a session needs a name")
	case len(name) > maxSessionName:
		return fmt.Errorf("a session name can be at most %d characters", maxSessionName)
	case !sessionNamePattern.MatchString(name):
		return fmt.Errorf("%q is not a usable session name: start with a letter or digit, "+
			"then letters, digits, dots, dashes and underscores", name)
	case strings.HasSuffix(name, "."):
		// Windows drops a trailing dot from a directory name, so "spike." would
		// open the directory of "spike" while calling itself something else.
		return fmt.Errorf("%q is not a usable session name: it cannot end with a dot", name)
	case windowsDeviceName(name):
		return fmt.Errorf("%q is not a usable session name: Windows reserves it for a device", name)
	}
	return nil
}

// windowsDeviceName reports a name Windows resolves to a device rather than a
// file — CON, NUL, COM1 and the rest, in any case and with any extension, so
// "nul.txt" counts.
//
// Refused on every platform rather than only on Windows. A session name is a
// label a person picks once and then types for months; one that works on the
// Linux box and fails on the laptop would be learned at the worst time.
func windowsDeviceName(name string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// Session is one conversation in a project, as a listing shows it.
type Session struct {
	Name string
	// Current marks the session a bare `strument` would pick up.
	Current bool
	// Turns counts the turn rows across the session's segments, and Runs the
	// runs that recorded anything — see history.Runs.
	Turns int
	Runs  int
	// LastUsed is the newest modification time anywhere in the session, and
	// Bytes its size on disk. Blobs are not counted: they live at the project
	// level and are shared, so charging them to one session would be wrong
	// twice — the number would be too large, and deleting that session would
	// not reclaim it.
	LastUsed time.Time
	Bytes    int64
}

// ListSessions returns a project's sessions, oldest use last.
//
// Newest first, because the question this answers is "what was I doing", and
// the answer decays with age. A name that is not a usable session name is
// skipped rather than reported: it cannot have been created by Strument, and a
// listing is not the place to litigate what someone put in the directory.
func ListSessions(projectRoot string) ([]Session, error) {
	dir, err := artifactPath(projectRoot, artSessions)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	current := CurrentSession(projectRoot)

	out := make([]Session, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || ValidSessionName(e.Name()) != nil {
			continue
		}
		s := Session{Name: e.Name(), Current: e.Name() == current}
		sd, err := SessionDir(projectRoot, e.Name())
		if err != nil {
			continue
		}
		s.Bytes, s.LastUsed = subtree(sd)
		s.Runs, s.Turns = countTurns(projectRoot, e.Name())
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Session) int {
		if a.LastUsed.Equal(b.LastUsed) {
			return strings.Compare(a.Name, b.Name)
		}
		if a.LastUsed.After(b.LastUsed) {
			return -1
		}
		return 1
	})
	return out, nil
}

// countTurns counts a session's segments and the turn rows in them.
//
// It reads the records rather than stat-ing, which is affordable because the
// heavy payloads are not in them: Phase 3 moved tool results to the blob
// store, so a segment is the conversation's shape rather than its bulk. It
// scans for the row type rather than decoding, because a listing does not need
// the fields and a malformed tail should not cost a count.
func countTurns(projectRoot, session string) (runs, turns int) {
	segments, err := LogSegments(projectRoot, session)
	if err != nil {
		return 0, 0
	}
	for _, seg := range segments {
		data, err := os.ReadFile(seg)
		if err != nil {
			continue
		}
		// A run counts if it recorded anything past the header, the rule
		// Runs uses, so this count and the range of --back agree.
		ran := false
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.Contains(line, `"type":"turn"`) {
				turns++
			}
			if line != "" && !strings.Contains(line, `"type":"session"`) {
				ran = true
			}
		}
		if ran {
			runs++
		}
	}
	return runs, turns
}

// DeleteSession removes one session's directory and everything in it.
//
// The only deletion in here that is not a blob, and it happens because
// somebody asked for it by name. Nothing calls this on Strument's own
// initiative: the retention rule is that history is never dropped to save
// space, which is a rule about what the harness decides, not about what a
// person may do with their own files.
//
// Blobs are left alone. They are shared across sessions and named by their
// contents, so a sweep is the only correct way to reclaim them — see
// `strument history strip`.
func DeleteSession(projectRoot, session string) error {
	if err := ValidSessionName(session); err != nil {
		return err
	}
	dir, err := SessionDir(projectRoot, session)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no session named %q", session)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// The pointer must not outlive what it points at. Leaving it would make
	// the next run open a session by that name, recreate its directory, and
	// present an empty conversation as the one the user had been in.
	if CurrentSession(projectRoot) == session {
		return SetCurrentSession(projectRoot, DefaultSession)
	}
	return nil
}

// RenameSession moves a session's directory, and follows `current` if it
// pointed at the old name.
func RenameSession(projectRoot, from, to string) error {
	if err := ValidSessionName(from); err != nil {
		return err
	}
	if err := ValidSessionName(to); err != nil {
		return err
	}
	if from == to {
		return nil
	}
	src, err := SessionDir(projectRoot, from)
	if err != nil {
		return err
	}
	dst, err := SessionDir(projectRoot, to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("no session named %q", from)
	}
	// Refused rather than merged. Two sessions are two conversations, and
	// there is no way to interleave their records that would not invent a
	// history neither of them had.
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("a session named %q already exists", to)
	}
	// Read before the move, not after. CurrentSession checks that the session
	// it names still exists, and after the rename the old name does not — so
	// asking afterwards reports the default and the pointer never follows.
	wasCurrent := CurrentSession(projectRoot) == from
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	if wasCurrent {
		return SetCurrentSession(projectRoot, to)
	}
	return nil
}

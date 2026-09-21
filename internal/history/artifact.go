package history

import (
	"fmt"
	"path/filepath"
)

// artifact is one file a project's state directory can hold.
//
// The table below is the single place a state file is named, and every path
// accessor in this package goes through it. That is not tidiness: a project
// directory can be *merged* into another one when a renamed project is adopted
// (see adopt.go), and a merge needs a policy per file. A file added without a
// policy would be silently dropped on adopt — the kind of loss nobody notices
// until they go looking for something a year old. Making the name and the
// policy one declaration means you cannot add the first and forget the second.
//
// The remaining hole is a caller that joins onto ProjectDir's result by hand
// from another package. ProjectDir's doc says not to; review is what catches it.
type artifact struct {
	name string
	// dir marks an artifact that is a directory rather than a file.
	//
	// Every policy but mergeUnion starts with os.ReadFile, which on a
	// directory returns EISDIR — not os.ErrNotExist, so the missing-source
	// escape hatches in adopt.go do not fire and a merge aborts partway
	// through a map-ordered loop, having already rewritten an arbitrary
	// subset. A directory artifact must therefore carry mergeUnion, which
	// mergeArtifact checks rather than trusting the table to be written
	// carefully.
	dir    bool
	policy mergePolicy
	// why documents the policy for a reader of `strument project adopt`'s
	// plan, which prints it. A policy nobody can see the reason for is one
	// that gets changed by guess.
	why string
}

// mergePolicy says what adopting does with one artifact. There is no default:
// merge is exhaustive over the policies, so a new one has to be handled.
type mergePolicy int

const (
	// mergeAppend concatenates the two files. Only for append-only records
	// where a line means the same thing wherever it sits.
	mergeAppend mergePolicy = iota
	// mergeJSONLByTime concatenates JSON Lines and sorts by their "time"
	// field, so an orphan *newer* than its destination still lands in order.
	mergeJSONLByTime
	// keepNewest picks one whole file by its recorded "updated" timestamp.
	// For state that is one value, where there is no union to take.
	keepNewest
	// skipTransient leaves the destination's file alone and drops the
	// source's. For files that mean nothing outside a running session.
	skipTransient
	// rewritten is handled by the adopt itself, not by the merge — the
	// identity record is the thing being repaired.
	rewritten
	// mergeUnion copies the source directory's entries that the destination
	// does not already have. Only for a directory whose entry names carry
	// their own identity, so that two entries with one name are the same
	// thing rather than a collision: content-addressed blobs, and log
	// segments named by the instant they were opened.
	mergeUnion
	// mergeSessions unions a tree of session directories: a session only the
	// source has is copied whole, and a session both have merges artifact by
	// artifact under sessionArtifacts.
	//
	// Not mergeUnion, which would resolve a name collision by keeping the
	// destination's copy. That rule is sound only for names that identify
	// their own contents, and a session's name identifies a conversation, not
	// a state of one — two `default/resume.json` files are two different pin
	// sets, and picking by directory order rather than by timestamp would
	// silently discard the newer.
	mergeSessions
)

// dirPolicies are the policies that read a directory. A directory artifact
// must carry one and a file artifact must not, which
// TestArtifactKindMatchesItsPolicy enforces: every other policy begins with
// os.ReadFile, and on a directory that returns EISDIR rather than
// os.ErrNotExist, so adopt.go's missing-source escape hatches do not fire.
var dirPolicies = map[mergePolicy]bool{mergeUnion: true, mergeSessions: true}

// Artifact ids. Callers name these rather than the file names.
const (
	artTranscript = "transcript"
	artInput      = "input"
	artCost       = "cost"
	artRoot       = "root"
	artLock       = "lock"
	artDismissed  = "dismissed"
	artBlobs      = "blobs"
	artSessions   = "sessions"
	artCurrent    = "current"
)

// Session-level artifact ids, for the files inside sessions/<name>/.
const (
	sartResume = "resume"
	sartUndo   = "undo"
	sartLog    = "log"
)

// artifacts is every file Strument writes into a project's state directory.
//
// Adding one here is what makes it exist; adding one anywhere else is a bug
// TestProjectDirHoldsOnlyRegisteredArtifacts is there to catch.
var artifacts = map[string]artifact{
	artTranscript: {
		name: "transcript.md", policy: mergeAppend,
		why: "append-only prose; one turn after another reads the same either way",
	},
	artInput: {
		name: "input.txt", policy: mergeAppend,
		why: "a line list that already tolerates duplicates",
	},
	artCost: {
		name: "cost.jsonl", policy: mergeJSONLByTime,
		why: "one timestamped row per turn, so the two ledgers interleave by time",
	},
	artRoot: {
		name: "root", policy: rewritten,
		why: "the identity record being repaired",
	},
	artLock: {
		name: "lock", policy: skipTransient,
		why: "an advisory lock means nothing outside a running session",
	},
	artDismissed: {
		name: "dismissed", policy: mergeAppend,
		why: "orphans you said not to offer; both sides' answers stay true after a merge",
	},
	artBlobs: {
		name: "blobs", dir: true, policy: mergeUnion,
		why: "content-addressed, so one name is one payload and a union cannot conflict",
	},
	artSessions: {
		name: "sessions", dir: true, policy: mergeSessions,
		why: "a tree of conversations; a session both sides have merges by its own artifacts",
	},
	artCurrent: {
		name: "current", policy: keepNewest,
		why: "one value: the session a resume picks up, and two answers cannot both be it",
	},
}

// sessionArtifacts is every file Strument writes into one session's directory,
// under the same rule as artifacts: naming it here is what makes it exist, and
// mergeSessions walks this table when adopting a session both projects have.
var sessionArtifacts = map[string]artifact{
	sartResume: {
		name: "resume.json", policy: keepNewest,
		why: "one value: there is no meaningful union of two pinned file sets",
	},
	sartUndo: {
		name: "undo.json", policy: keepNewest,
		why: "a stack against one continuous tree history; two overlapping stacks cannot be ordered",
	},
	sartLog: {
		name: "log", dir: true, policy: mergeUnion,
		why: "segments named by the instant they were opened, so one name is one run",
	},
}

// artifactPath is the one way to build a path inside a project's state
// directory.
func artifactPath(projectRoot, id string) (string, error) {
	a, ok := artifacts[id]
	if !ok {
		// A programming error, not a runtime condition: the ids are constants
		// in this file. Saying which one keeps the panic legible.
		return "", fmt.Errorf("no such state artifact %q", id)
	}
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, a.name), nil
}

// DefaultSession is the session a project has before anyone names another, and
// the one a resume picks up when `current` is missing or unreadable.
const DefaultSession = "default"

// SessionDir is one session's directory inside a project's state directory.
// It does not create anything; EnsureSessionDir does.
func SessionDir(projectRoot, session string) (string, error) {
	if session == "" {
		session = DefaultSession
	}
	dir, err := artifactPath(projectRoot, artSessions)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, session), nil
}

// sessionArtifactPath is the one way to build a path inside a session's
// directory, for the same reason artifactPath is: a name that skipped the
// table is a name `strument project adopt` would drop.
func sessionArtifactPath(projectRoot, session, id string) (string, error) {
	a, ok := sessionArtifacts[id]
	if !ok {
		return "", fmt.Errorf("no such session artifact %q", id)
	}
	dir, err := SessionDir(projectRoot, session)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, a.name), nil
}

// artifactNames lists every registered file name, for callers that need to
// recognize what belongs in a project directory.
func artifactNames() map[string]string {
	return namesOf(artifacts)
}

// sessionArtifactNames is artifactNames for one session's directory.
func sessionArtifactNames() map[string]string {
	return namesOf(sessionArtifacts)
}

func namesOf(table map[string]artifact) map[string]string {
	out := make(map[string]string, len(table))
	for id, a := range table {
		out[a.name] = id
	}
	return out
}

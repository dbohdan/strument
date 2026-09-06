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
	name   string
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
)

// Artifact ids. Callers name these rather than the file names.
const (
	artTranscript = "transcript"
	artInput      = "input"
	artCost       = "cost"
	artResume     = "resume"
	artUndo       = "undo"
	artRoot       = "root"
	artLock       = "lock"
	artDismissed  = "dismissed"
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
	artResume: {
		name: "resume.json", policy: keepNewest,
		why: "one value: there is no meaningful union of two pinned file sets",
	},
	artUndo: {
		name: "undo.json", policy: keepNewest,
		why: "a stack against one continuous tree history; two overlapping stacks cannot be ordered",
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

// artifactNames lists every registered file name, for callers that need to
// recognize what belongs in a project directory.
func artifactNames() map[string]string {
	out := make(map[string]string, len(artifacts))
	for id, a := range artifacts {
		out[a.name] = id
	}
	return out
}

package coder

import (
	"errors"
	"fmt"
	"slices"
)

// /rewind takes the last turns out of the conversation, for when a turn went
// wrong in a way that would steer the next one: a model that came to believe it
// had four tools, say, and kept acting on it.
//
// The record is append-only, so nothing is deleted. A rewind appends a
// tombstone row that restore applies in order (applyRewinds), and the turns
// stay in the record for the transcript and for anyone reading it later.
//
// Only from the end. Rewinding a suffix keeps every tool call paired with its
// result, keeps the cached prefix of the next request, and leaves "the last n
// turns" unambiguous without turn ids.
//
// The files are not touched. The model's picture of the tree goes stale in
// every session anyway — the user edits, pulls, formats — and /undo is the
// command for edits. What a rewind does say is which files the rewound turns
// changed, because the model is losing its memory of changing them.

// TurnSpan is one turn's messages in the settled history: [Start, End), and
// the files the turn changed.
type TurnSpan struct {
	Start, End int
	Files      []string
}

// RewindResult is what a rewind did, for the line the user is shown.
type RewindResult struct {
	Turns int
	Files []string
}

// errNothingToRewind is a rewind with no turn in the history to take out.
var errNothingToRewind = errors.New("there is no turn to rewind")

// Rewind takes the last n turns out of the conversation and records a
// tombstone so a restore does the same.
//
// Turns folded into a compaction summary cannot be taken out: the summary
// already absorbed them. A rewind that reaches past the compaction is refused
// with that reason rather than trimmed to what is left, since "rewind 3" that
// silently rewound 1 would be the worse surprise.
func (c *Coder) Rewind(n int) (RewindResult, error) {
	if n < 1 {
		return RewindResult{}, errors.New("rewind takes a positive number of turns")
	}
	if len(c.curMessages) > 0 {
		return RewindResult{}, errors.New("a turn is in progress")
	}
	if len(c.turns) == 0 {
		if c.compactedTurns {
			return RewindResult{}, errors.New("the history was compacted, and the turns in the summary " +
				"cannot be taken back out. /session fork or /clear starts afresh")
		}
		return RewindResult{}, errNothingToRewind
	}
	if n > len(c.turns) {
		why := ""
		if c.compactedTurns {
			why = "; the ones before them were compacted into a summary"
		}
		return RewindResult{}, fmt.Errorf("only %s can be rewound%s",
			plural(len(c.turns), "turn", "turns"), why)
	}
	gone := c.turns[len(c.turns)-n:]
	from, to := gone[0].Start, gone[len(gone)-1].End
	if from < 0 || to > len(c.doneMessages) || from > to {
		// The spans and the history disagree, which only a bug makes. Refusing
		// is safer than cutting the wrong messages out of a conversation.
		return RewindResult{}, errors.New("the history does not match its turn record; nothing was rewound")
	}
	var files []string
	for _, t := range gone {
		for _, f := range t.Files {
			if !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
	}
	// Anything after the last turn stays: a harness note added between turns,
	// such as the one /undo leaves, describes the tree, not the conversation.
	tail := slices.Clone(c.doneMessages[to:])
	c.doneMessages = append(c.doneMessages[:from], tail...)
	c.turns = c.turns[:len(c.turns)-n]
	c.record(Record{Type: "rewind", Rewound: n})
	slices.Sort(files)
	return RewindResult{Turns: n, Files: files}, nil
}

// TurnCount is how many turns /rewind could take out now.
func (c *Coder) TurnCount() int { return len(c.turns) }

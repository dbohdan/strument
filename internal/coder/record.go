// A machine-readable record of what a session actually did.
//
// This exists because measurement kept failing on parsing. Eleven scorer bugs
// on this project so far, and the largest cluster is one shape: a check reading
// rendered terminal output and picking the wrong region of it. An ANSI escape
// landing at the start of an answer line turned a real effect into a clean
// p=1.0 null (doc/experimenting.md §1). A reasoning renderer with two block
// forms deleted the final answer of every run whose last aside was the one-line
// kind (§15). "Committed " was counted in a transcript it never reaches,
// because tool *results* are not printed at all. None of those are mistakes
// about the code under test; they are mistakes about where the text was.
//
// So this is not the rendered stream with the colours off. It is the
// conversation as the model received it — every message, in order, with roles,
// tool calls, their arguments, and their results — plus the reasoning, which is
// the thing a scorer most needs held apart from the answer.
//
// What it deliberately does not do is replace the terminal. Output stays what
// the human reviews, and `--jsonl` is a second sink beside it rather than a
// mode: an experiment that wants to check what the user *saw* can still read
// the rendered stream, which matters because moving all measurement off the
// rendered path would retire a canary. §1's bug was found because it broke a
// scorer.

package coder

import (
	"slices"

	"dbohdan.com/strument/internal/llm"
)

// RecordVersion is the schema version carried by the session record. Consumers
// live outside this repository, so a change to a field's meaning needs a number
// they can branch on.
const RecordVersion = 1

// Record is one line of the JSONL log. It is deliberately one flat struct with
// a Type discriminator rather than a family of types: the consumer is usually
// three lines of jq or Python, and a flat shape keeps `select(.type=="message")`
// the whole of the parsing.
type Record struct {
	Type string `json:"type"`

	// session
	Version    int    `json:"version,omitempty"`
	Model      string `json:"model,omitempty"`
	Root       string `json:"root,omitempty"`
	EditFormat string `json:"edit_format,omitempty"`

	// message
	Role string `json:"role,omitempty"`
	Text string `json:"text,omitempty"`
	// ToolCalls carries the arguments as the model sent them, which the
	// rendered stream shows only as a one-line summary. A scorer counting
	// "did it search for X" needs the argument, not the summary — one of the
	// eleven counted "FINISHED" as a result when it was in the command string.
	ToolCalls []RecordToolCall `json:"tool_calls,omitempty"`
	// ToolCallID is set on a tool-result message and matches the call it
	// answers, so results can be paired with arguments without guessing.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// reasoning
	//
	// Its own record type rather than a field on the assistant message,
	// because the whole point is that it is *not* the answer. A scorer that
	// cannot tell them apart credits a model that worked the answer out and
	// then failed to give it.

	// turn
	Outcome   string  `json:"outcome,omitempty"`
	Steps     int     `json:"steps,omitempty"`
	Sent      int     `json:"sent,omitempty"`
	Received  int     `json:"received,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
	CostKnown bool    `json:"cost_known,omitempty"`
	// Pinned lets a consumer distinguish files the model could see from files
	// it never saw when auditing whether a file was read before it was edited.
	Pinned []string `json:"pinned,omitempty"`
	// EditsExact and EditsFuzzy split the turn's applied edits by how the
	// text was found: verbatim, or by the line matcher guessing which lines
	// were meant. The split is the measurement behind whether that guessing
	// still earns its keep — see
	// doc/experiments/2026-09-anchored-edit-preregistration.md, M9.
	EditsExact int `json:"edits_exact,omitempty"`
	EditsFuzzy int `json:"edits_fuzzy,omitempty"`
}

// RecordToolCall is one call the model made, with its arguments verbatim.
type RecordToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// Recorder receives the records. A nil Recorder on the Coder means no log,
// which is the default and costs nothing.
type Recorder interface {
	Record(r Record)
}

// record emits one record if a Recorder is wired.
func (c *Coder) record(r Record) {
	if c.Recorder == nil {
		return
	}
	c.Recorder.Record(r)
}

func (c *Coder) pinnedRecordPaths() []string {
	paths := make([]string, 0, len(c.absFnames)+len(c.absReadOnlyFnames))
	seen := make(map[string]bool, cap(paths))
	for _, abs := range append(append([]string{}, c.absFnames...), c.absReadOnlyFnames...) {
		path := c.displayName(abs)
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

// RecordSession writes the header record. Called once, by whoever built the
// Coder, because only they know the model alias the user actually typed.
func (c *Coder) RecordSession(modelName string) {
	c.record(Record{
		Type:       "session",
		Version:    RecordVersion,
		Model:      modelName,
		Root:       c.Root,
		EditFormat: c.editFormat,
	})
}

// recordNewMessages emits every message added to the current turn since the
// last call.
//
// Called after a send returns rather than as messages are appended, and that
// timing is the point: an interrupt runs dropPartialToolCalls, which *removes*
// an assistant message's tool calls and can drop the message outright. Emitting
// on append would put a message in the log that the conversation does not
// contain. sendMessage does its interrupt handling before returning, so by then
// the turn's messages have settled.
//
// The cost is that this is a log to read afterwards, not a live stream. If a
// live one is ever wanted, the retraction problem above is the thing to solve
// first.
func (c *Coder) recordNewMessages() {
	if c.Recorder == nil {
		return
	}
	// Reasoning is per-send and overwritten by the next one, so it is emitted
	// here or not at all. It is not part of any message: the assembler strips
	// it, because it is not something to send back.
	//
	// It goes immediately before the assistant message it preceded, which is
	// neither end of this flush. One flush can hold the user's turn, then the
	// model's reply, then the results of that reply's tool calls — so emitting
	// reasoning first would put it ahead of the user message that prompted it,
	// and emitting it last would put it after tool results it never saw. The
	// log is a timeline or it is a pile.
	pending := c.partialReasoningContent
	emitReasoning := func() {
		if pending != "" {
			c.record(Record{Type: "reasoning", Text: pending})
			pending = ""
		}
	}

	for _, m := range c.curMessages[min(c.recordedMessages, len(c.curMessages)):] {
		if m.Role == llm.RoleAssistant {
			emitReasoning()
		}
		r := Record{Type: "message", Role: m.Role, Text: m.Text(), ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, RecordToolCall{
				ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
			})
		}
		c.record(r)
	}
	// A send interrupted before it produced anything leaves reasoning with no
	// assistant message to sit in front of. It is still what happened.
	emitReasoning()
	c.recordedMessages = len(c.curMessages)
}

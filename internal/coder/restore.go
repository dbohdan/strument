package coder

import (
	"fmt"

	"dbohdan.com/strument/internal/llm"
)

// Rebuilding a conversation from the session record, so a session survives the
// process that had it.
//
// Called "restore" rather than "replay" because this package already uses
// replay for something else — the fixture harness that re-runs the coder
// against recorded streams (replay_test.go). This is the other direction:
// messages that were already exchanged, handed back as history.
//
// What this reverses is a position the project held deliberately, and the
// arguments against it were good ones. notes.go still carries them: cost,
// because a restored history is re-sent with every message of the next
// session; attention, because everything in the window influences the output;
// and attribution, because messages labelled `assistant` are read by the next
// model as its own past self, "so it will rationalize and then defend choices
// it would never have made, with no seam anywhere to notice".
//
// Cost and attention are answered by compaction, which the restored history
// goes through like any other: RestoreHistory runs the summarizer before the
// first send rather than waiting for the end of a turn, because the turn that
// would trigger it is the one that would fail.
//
// Attribution is answered by saying so. The objection is precisely that there
// is no seam; when the conversation was made by a different model, this puts
// one there. It is a harness note, in the voice the harness already uses to
// say that a reply was interrupted or a tool call was not run.

// RestoreStats is what a restore did, for the line the user is shown and for
// the tests that would otherwise have to infer it.
type RestoreStats struct {
	// Messages is how many were rebuilt, after repairs.
	Messages int
	// Dropped counts messages the record held that could not go back into a
	// conversation: an unanswered tool call, a system message where no
	// provider would accept one. A number worth surfacing, because it is the
	// difference between the record and what the model will see.
	Dropped int
	// Models is every distinct model that produced an assistant turn in the
	// restored conversation, in first-seen order, as the turn rows recorded it.
	// A turn row names its model only when the conversation it accounts for
	// still holds an assistant message: an interrupted turn that was cut off
	// before anything of the model's was recorded names a model the restored
	// conversation never shows, and attributing the seam note to it would
	// claim turns that are not there.
	Models []string
	// Turns are the surviving turns' ranges in the returned messages, oldest
	// first, so a restored session can be rewound like a live one.
	Turns []TurnSpan
	// Rewound counts the turns /rewind records took out.
	Rewound int
}

// MessagesFromRecords rebuilds a conversation from a record stream.
//
// Pure: records in, messages out, no file access. That is what keeps it in
// this package, where the invariants a conversation has to satisfy are
// written, rather than in internal/history, which owns reading the record and
// cannot import this package anyway.
//
// Rows that are not messages are skipped. The turn rows are the transcript's
// view of the same conversation and would duplicate it; the reasoning rows are
// deliberately not sent back, which recordNewMessages already says — "it is
// not part of any message: the assembler strips it, because it is not
// something to send back".
func MessagesFromRecords(records []Record) ([]llm.Message, RestoreStats) {
	var out []llm.Message
	var stats RestoreStats
	seenModel := map[string]bool{}

	// Which calls of the assistant message currently being answered are still
	// open. Scoped to that message, not to the record, and the difference is
	// not theoretical: ids repeat. A provider need only make them unique
	// within a request, and a record spans every request a session ever made
	// — so a global map lets a later result answer an earlier call of the
	// same name, restoring an unanswered call as though it were fine and
	// making the next request malformed. Found by running it against a stub
	// that reuses one id, which is the cheap version of a provider that
	// happens to.
	// Counted, not a set: one result answers one call. Two calls sharing an
	// id and one result between them is the same defect as a call with no
	// result at all, and a set cannot tell them apart.
	open := map[string]int{}
	// The notes for calls whose arguments were pruned, scoped like open: they
	// go in front of the result answering that call.
	pruned := map[string]string{}

	records = applyRewinds(records, &stats)
	turnStart := 0
	for i, r := range records {
		if r.Type == "turn" {
			// A turn row closes the messages recorded since the previous one:
			// the same grouping applyRewinds uses, so a turn restored here is
			// the turn a later /rewind will take out.
			if len(out) > turnStart {
				stats.Turns = append(stats.Turns, TurnSpan{Start: turnStart, End: len(out), Files: r.Files})
			}
			turnStart = len(out)
			continue
		}
		if r.Type != "message" {
			continue
		}
		// A tool result answers the calls of the assistant message it
		// follows, so the window closes at the next message that is not one.
		if r.Role != llm.RoleTool {
			open = map[string]int{}
			pruned = map[string]string{}
		}
		switch r.Role {
		case llm.RoleUser:
			// A conversation starts with the user. Anything before the first
			// one is a fragment of a record that was cut or edited, and there
			// is no shape to give it.
			out = append(out, llm.TextMessage(llm.RoleUser, r.Text))

		case llm.RoleAssistant:
			answered := answeredAfter(records, i)
			msg := llm.Message{Role: llm.RoleAssistant, Content: llm.TextContent(r.Text)}
			for _, tc := range r.ToolCalls {
				if answered[tc.ID] == 0 {
					continue
				}
				answered[tc.ID]--
				open[tc.ID]++
				if tc.Pruned != "" {
					pruned[tc.ID] = tc.Pruned
				}
				msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
					ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
				})
			}
			// An assistant message left with neither text nor calls is not a
			// turn; it is the shell of one whose calls were all unanswered.
			if r.Text == "" && len(msg.ToolCalls) == 0 {
				stats.Dropped++
				continue
			}
			if len(msg.ToolCalls) < len(r.ToolCalls) {
				stats.Dropped += len(r.ToolCalls) - len(msg.ToolCalls)
			}
			out = append(out, msg)
			// Attribution at the point of survival, not from the turn row:
			// a turn row follows the messages it accounts for, so this is
			// where "this model said something the conversation still holds"
			// is actually known. The turn that names the model could be
			// interrupted with nothing recorded, and a seam note pointing at
			// turns that are not there would be worse than none.
			if model := nextTurnModel(records, i); model != "" && !seenModel[model] {
				seenModel[model] = true
				stats.Models = append(stats.Models, model)
			}

		case llm.RoleTool:
			// A result whose call did not make it back is an orphan, and a
			// tool_call_id no request contains is as malformed as an
			// unanswered call. Answering the same call twice is the same
			// defect, so a call leaves the open set once it is answered.
			if open[r.ToolCallID] == 0 {
				stats.Dropped++
				continue
			}
			open[r.ToolCallID]--
			text := r.Text
			if note := pruned[r.ToolCallID]; note != "" {
				text = note + "\n\n" + text
				delete(pruned, r.ToolCallID)
			}
			out = append(out, llm.ToolResult(r.ToolCallID, text))

		case llm.RoleSystem:
			// The one system message that legitimately sits inside a
			// conversation is the context-exhausted note, and it is appended
			// only when the message before it is the user's. Anthropic
			// rejects a system message that follows an assistant turn
			// outright, so a restore that put one back anywhere else would
			// end the session on its first request.
			if len(out) == 0 || out[len(out)-1].Role != llm.RoleUser {
				stats.Dropped++
				continue
			}
			out = append(out, llm.TextMessage(llm.RoleSystem, r.Text))

		default:
			stats.Dropped++
		}
	}

	before := len(out)
	out = trimToFirstUserTurn(out, &stats)
	if cut := before - len(out); cut > 0 {
		kept := stats.Turns[:0]
		for _, t := range stats.Turns {
			t.Start, t.End = max(t.Start-cut, 0), t.End-cut
			if t.End > t.Start {
				kept = append(kept, t)
			}
		}
		stats.Turns = kept
	}
	stats.Messages = len(out)
	return out, stats
}

// applyRewinds takes out the turns /rewind records removed, before anything
// else reads the records.
//
// The record is append-only, so a rewind is a tombstone: {"type":"rewind",
// "rewound":n} says the n turns before it are no longer in the conversation.
// A turn is the rows recorded since the previous turn row, up to and including
// its own: the grouping the live history uses, where a turn's messages move
// into history together at its end. Rows after the last turn row belong to no
// closed turn and are never taken out by a later rewind; they only become part
// of the next turn. Tombstones apply in order, so a rewind of a rewind's
// survivors works as the user saw it.
//
// Working on rows rather than on rebuilt messages leaves everything downstream
// as it was: the repairs, the model attribution and the trimming see a record
// in which the rewound turns never happened.
func applyRewinds(records []Record, stats *RestoreStats) []Record {
	hasRewind := false
	for _, r := range records {
		if r.Type == "rewind" {
			hasRewind = true
			break
		}
	}
	if !hasRewind {
		return records
	}
	type group struct{ from, to int } // record indices, [from, to)
	var closed []group
	drop := make([]bool, len(records))
	open := 0
	for i, r := range records {
		switch r.Type {
		case "turn":
			closed = append(closed, group{open, i + 1})
			open = i + 1
		case "rewind":
			drop[i] = true
			n := min(r.Rewound, len(closed))
			for _, g := range closed[len(closed)-n:] {
				for j := g.from; j < g.to; j++ {
					drop[j] = true
				}
			}
			closed = closed[:len(closed)-n]
			stats.Rewound += n
			open = i + 1
		}
	}
	out := make([]Record, 0, len(records))
	for i, r := range records {
		if !drop[i] {
			out = append(out, r)
		}
	}
	return out
}

// answeredAfter counts, per call id, the tool results in the run immediately
// following the message at i.
//
// "Immediately following" is the whole point. A tool result belongs to the
// assistant message it answers, and scanning past the next non-tool message
// would let one send's results vouch for another send's calls — which is how
// a repeated id turns an unanswered call into a restored one.
func answeredAfter(records []Record, i int) map[string]int {
	answered := map[string]int{}
	for _, r := range records[i+1:] {
		if r.Type != "message" {
			continue // a reasoning or turn row does not end the run
		}
		if r.Role != llm.RoleTool {
			break
		}
		if r.ToolCallID != "" {
			answered[r.ToolCallID]++
		}
	}
	return answered
}

// nextTurnModel is the model of the turn row nearest after the message at i,
// or "" when the message is not followed by one.
//
// A record is segmented: the messages of a turn, then the turn row that
// accounts for them. The model a request ran under is not on the message rows
// themselves — it is a property of the turn, so the attribution walks forward
// to the row that closes it. answeredAfter already commits this package to
// reading that shape (a turn row does not end a run of tool results), and the
// recorder writes the rows in this order; a record that held a turn row in the
// middle of a tool-run would attribute to the wrong turn, which is a shape
// nothing writes today and the note would misfire on.
//
// A model is only ever attributed once the assistant message it produced has
// survived into the restored conversation — the caller checks that — because
// the alternative is a seam note about turns the conversation does not
// contain: the exactly-backwards failure of a note whose whole job is
// attribution.
func nextTurnModel(records []Record, i int) string {
	for _, r := range records[i+1:] {
		if r.Type == "turn" {
			return r.Model
		}
		if r.Type == "message" && r.Role == llm.RoleUser {
			// The next turn's messages began: this message was never
			// accounted for.
			return ""
		}
	}
	return ""
}

// trimToFirstUserTurn drops anything before the conversation's first user
// message.
//
// A whole record never needs this: a turn opens with what the user typed. One
// that has been edited by hand does — `strument history edit` exists so that
// something which should not have been recorded can be taken out, and taking
// out the first prompt leaves the reply to it at the front.
func trimToFirstUserTurn(msgs []llm.Message, stats *RestoreStats) []llm.Message {
	for i, m := range msgs {
		if m.Role == llm.RoleUser {
			stats.Dropped += i
			return msgs[i:]
		}
	}
	stats.Dropped += len(msgs)
	return nil
}

// RestoreHistory installs a rebuilt conversation as settled history and
// compacts it if it is already too big to send.
//
// Into doneMessages rather than curMessages, and the difference is not
// bookkeeping. curMessages is the turn in progress: the recorder's watermark
// walks it, so a conversation seeded there would be written into this run's
// segment as though it had just happened, duplicating the whole history every
// time a session was resumed.
//
// Compaction runs here rather than at the end of the first turn because the
// first turn is the one that would fail. That is aider #2979 — an 80k-token
// restored history that fails on the wire, where configuring a summarizer
// does not help because compaction fires at a turn boundary and there has not
// been one yet. CheckRestoredContext warns about the same trap and used to be
// able to say Strument restored less than a conversation; it no longer can.
func (c *Coder) RestoreHistory(msgs []llm.Message, turns []TurnSpan) {
	if len(msgs) == 0 {
		return
	}
	c.doneMessages = msgs
	c.turns = turns
	c.maybeSummarize()
}

// NoteRestoredFromAnotherModel puts a seam in a conversation the current model
// did not have.
//
// This is the answer to the attribution objection in notes.go, which is the
// strongest argument against restoring a conversation at all: an assistant
// turn is read by the next model as its own past self, and it will defend
// choices it would never have made because nothing tells it they were not its
// own. Saying so is cheap and is the whole of what was missing.
//
// It names no model, for the reason the notes header names none: the fact that
// matters is "not you", and a name invites the reader to weigh what it finds
// by whose it was rather than by what it says. The record keeps the model on
// every turn, so a person can always see which.
//
// A user-role harness note, not a system message: the system role belongs to
// the prefix, and this lands after an assistant turn, which is exactly where
// Anthropic rejects one.
func (c *Coder) NoteRestoredFromAnotherModel() {
	c.doneMessages = append(c.doneMessages, llm.HarnessNote(
		"The turns above are from earlier in this session, made by a different model. "+
			"They are a record of what happened, not decisions you made — where you have "+
			"reason to disagree with them, say so rather than defending them."))
}

// RestoredFromAnotherModel reports whether a restored conversation was made by
// any model other than the one now running.
//
// Compares the qualified slug, which is what the turn rows record. A session
// whose model was switched mid-way has several, and one of them differing is
// enough: the seam is about the assistant turns not being this model's, and
// some of them are not.
func (c *Coder) RestoredFromAnotherModel(stats RestoreStats) bool {
	now := c.Model.QualifiedSlug()
	for _, m := range stats.Models {
		if m != now {
			return true
		}
	}
	return false
}

// RestoreNote is the line the user is shown, or "" when there is nothing to
// say. Phrased like the pin-restoring line beside it.
func (s RestoreStats) RestoreNote() string {
	if s.Messages == 0 {
		return ""
	}
	note := fmt.Sprintf("Restored %d %s from this session.", s.Messages,
		map[bool]string{true: "message", false: "messages"}[s.Messages == 1])
	if s.Dropped > 0 {
		// Said rather than swallowed: the difference between what the record
		// holds and what the model will see is the kind of thing that is
		// invisible until it matters.
		note += fmt.Sprintf(" %d could not be restored.", s.Dropped)
	}
	return note
}

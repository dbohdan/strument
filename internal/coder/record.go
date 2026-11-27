// Writing the session record: see internal/record for what it is and why.

package coder

import (
	"strings"
	"time"

	"dbohdan.com/strument/internal/record"

	"dbohdan.com/strument/internal/llm"
)

// Recording reports whether this run's records are being kept. A record.Recorder
// can be wired and still write nothing: the session log closes its segment
// when a switch cannot open the next one, and the recorder stays in place. One
// that can tell says so through a Recording method.
func (c *Coder) Recording() bool {
	if c.Recorder == nil {
		return false
	}
	if r, ok := c.Recorder.(interface{ Recording() bool }); ok {
		return r.Recording()
	}
	return true
}

// BlobStore stores a payload and returns the name to find it under.
//
// A callback rather than a path, for the reason RecordUsage and SaveUndo are:
// the coder never learns where state lives. nil is the default and keeps every
// payload inline, which is what a session that leaves no trace wants — there
// is nowhere to put a blob when there is no project directory.
type BlobStore func(data []byte) (string, error)

// offload decides a payload's fate and fills in whichever fields apply.
//
// setInline and setBlob are passed rather than returned into, because the two
// callers write into different structs — a record.Record and a record.ToolCall — that
// carry the same four fields for the same reason but are not the same type.
//
// A store that fails keeps the payload inline and says nothing. The record is
// a thing to read afterwards: a turn that worked must not fail, and must not
// print a warning, because the blob store being full is not news the person
// mid-conversation can act on. The cost of the quiet fallback is a larger
// record, which is the state everything was in before this existed.
func (c *Coder) offload(tool, payload string, setInline func(string), setBlob func(blob string, size int, summary string)) {
	if c.PutBlob == nil || !storeSeparately(tool, len(payload)) {
		setInline(payload)
		return
	}
	hash, err := c.PutBlob([]byte(payload))
	if err != nil || hash == "" {
		setInline(payload)
		return
	}
	setBlob(hash, len(payload), payloadSummary(payload))
}

// isConversation reports whether m is part of the conversation proper — the
// model's answer, or what the user typed — rather than payload. The
// conversation is never stored separately, whatever its length.
//
// It went to the blob store once it passed the floor, because a message
// with no tool name took the default rule, and a prune could then delete an
// answer while the reasoning before it, recorded inline, stayed. The store is
// for what the model read and ran; ask_user_question is in inlineTools for
// the same reason this exists. A user-role message the harness wrote — /run
// output added to the chat, a lint report — is payload, and keeps the rule.
func isConversation(m llm.Message) bool {
	switch m.Role {
	case llm.RoleAssistant:
		return true
	case llm.RoleUser:
		return !strings.HasPrefix(m.Text(), llm.HarnessMarker)
	}
	return false
}

// recordCompaction logs one fold of the history and the text that replaced
// the folded turns.
func (c *Coder) recordCompaction(mode, outcome, text string, before, after int) {
	c.record(record.Record{Type: record.KindCompaction, Mode: mode, Outcome: outcome, Text: text, TokensBefore: before, TokensAfter: after})
}

// streamOutcomes are the request record's words for a streamResult.
var streamOutcomes = map[streamResult]string{
	resDone:             "done",
	resContinuation:     "continuation",
	resContextExhausted: "context_exhausted",
	resOutputExhausted:  "output_exhausted",
	resInterrupted:      "interrupted",
	resLooping:          "looping",
	resFailed:           "failed",
}

// recordRequest logs one request streamOnce made. usage is what the provider
// reported for this request alone; sawUsage says whether it reported any, so
// a request that failed before usage arrived records no counts rather than
// zeroes that look measured.
func (c *Coder) recordRequest(call string, res streamResult, err error, finishReason string,
	usage llm.Usage, sawUsage bool, elapsed time.Duration, estimated int,
) {
	if c.Recorder == nil {
		return
	}
	r := record.Record{
		Type:         record.KindRequest,
		Call:         call,
		Model:        c.Model.QualifiedSlug(),
		Outcome:      streamOutcomes[res],
		FinishReason: finishReason,
		Seconds:      elapsed.Round(time.Millisecond).Seconds(),
	}
	if call == "turn" {
		r.Step = c.turnSteps
	}
	if err != nil {
		r.Error = err.Error()
	}
	if sawUsage {
		r.Sent = usage.PromptTokens
		r.Estimated = estimated
		r.Received = usage.CompletionTokens
		r.CacheRead = usage.CacheReadTokens
		r.CacheWrite = usage.CacheWriteTokens
		r.Reasoning = usage.ReasoningTokens
		r.Provider = usage.Provider
		if usage.Cost != nil {
			r.Cost, r.CostKnown = *usage.Cost, true
		}
	}
	// An aside's messages never pass through recordNewMessages, so there is
	// no later flush to wait for.
	if call != "turn" {
		c.record(r)
		return
	}
	c.pendingRequests = append(c.pendingRequests, r)
}

// record emits one record if a record.Recorder is wired.
func (c *Coder) record(r record.Record) {
	if c.Recorder == nil {
		return
	}
	c.Recorder.Record(r)
}

// RecordSession writes the header record. Called once, by whoever built the
// Coder, because only they know the model alias the user actually typed.
func (c *Coder) RecordSession(modelName string) {
	c.record(record.Record{
		Type:    record.KindSession,
		Version: record.Version,
		Model:   modelName,
		Root:    c.Root,
	})
}

// RecordModelSwitch notes a /model switch, naming the new model the way the
// session header names the first: by alias. Without it the header was the only
// place a run named its model, so a run started on one model and switched to
// another read, to anyone looking at the record, as the first model's work.
// The turn rows carry the qualified slug either way; this is for the reader
// who opens the log at the top.
func (c *Coder) RecordModelSwitch(alias string) {
	c.record(record.Record{Type: record.KindModel, Model: alias})
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
	// here or not at all. It is recorded as its own row rather than on the
	// message: a model with replay_reasoning carries it on the live message,
	// but a restore does not send it back (MessagesFromRecords).
	//
	// It goes immediately before the assistant message it preceded, which is
	// neither end of this flush. One flush can hold the user's turn, then the
	// model's reply, then the results of that reply's tool calls — so emitting
	// reasoning first would put it ahead of the user message that prompted it,
	// and emitting it last would put it after tool results it never saw. The
	// log is a timeline or it is a pile.
	pending := c.stream.reasoning
	emitReasoning := func() {
		if pending != "" {
			c.record(record.Record{Type: record.KindReasoning, Text: pending})
			pending = ""
		}
	}

	// A tool result names the call it answers but not the tool that ran, and
	// the policy needs the name. The assistant message that made the call and
	// the results answering it arrive in one flush — sendMessage appends both
	// before returning — so a map built as this loop goes is enough. A name it
	// does not find gets the default rule, which differs only for tools whose
	// results are short anyway.
	toolNames := map[string]string{}

	// The send's requests follow the reply they produced, for the same reason
	// reasoning precedes it: the first assistant message of the flush is the
	// reply, and what comes after it — tool results — the requests never saw.
	emitRequests := func() {
		for _, r := range c.pendingRequests {
			c.record(r)
		}
		c.pendingRequests = nil
	}

	for _, m := range c.curMessages[min(c.recordedMessages, len(c.curMessages)):] {
		if m.Role == llm.RoleAssistant {
			emitReasoning()
		}
		r := record.Record{Type: record.KindMessage, Role: m.Role, ToolCallID: m.ToolCallID}
		if isConversation(m) {
			r.Text = m.Text()
		} else {
			c.offload(toolNames[m.ToolCallID], m.Text(),
				func(text string) { r.Text = text },
				func(blob string, size int, summary string) {
					r.Blob, r.Bytes, r.Summary = blob, size, summary
				})
		}
		for _, tc := range m.ToolCalls {
			toolNames[tc.ID] = tc.Name
			rec := record.ToolCall{ID: tc.ID, Name: tc.Name}
			c.offload(tc.Name, tc.Arguments,
				func(args string) { rec.Arguments = args },
				func(blob string, size int, summary string) {
					rec.Blob, rec.Bytes, rec.Summary = blob, size, summary
				})
			r.ToolCalls = append(r.ToolCalls, rec)
		}
		c.record(r)
		if m.Role == llm.RoleAssistant {
			emitRequests()
		}
	}
	// A send interrupted before it produced anything leaves reasoning with no
	// assistant message to sit in front of. It is still what happened. The same
	// goes for a request that failed outright.
	emitReasoning()
	emitRequests()
	c.recordedMessages = len(c.curMessages)
}

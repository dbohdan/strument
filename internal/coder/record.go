// A machine-readable record of what a session actually did.
//
// This exists because measurement kept failing on parsing. Eleven scorer bugs
// on this project so far, and the largest cluster is one shape: a check reading
// rendered terminal output and picking the wrong region of it. An ANSI escape
// landing at the start of an answer line turned a real effect into a clean
// p=1.0 null (doc/experimenting.md#instrument-is-the-system). A reasoning
// renderer with two block
// forms deleted the final answer of every run whose last aside was the one-line
// kind (#renderer-has-two-forms). "Committed " was counted in a transcript it
// never reaches,
// because tool *results* are not printed at all. None of those are mistakes
// about the code under test; they are mistakes about where the text was.
//
// So this is not the rendered stream with the colours off. It is the
// conversation as the model received it — every message, in order, with roles,
// tool calls, their arguments, and their results — plus the reasoning, which is
// the thing a scorer most needs held apart from the answer.
//
// What it deliberately does not do is replace the terminal. Output stays what
// the human reviews, and the session log is a second sink beside it rather
// than a mode: an experiment that wants to check what the user *saw* can still
// read the rendered stream, which matters because moving all measurement off
// the rendered path would retire a canary. The ANSI bug was found because it
// broke a scorer.

package coder

import (
	"slices"
	"strings"
	"time"

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

	// Blob, Bytes and Summary describe a payload that is stored separately,
	// under internal/history's blob store, instead of inline in Text. Set
	// together or not at all: a record that has them has no Text, and one
	// that has Text has none of them.
	//
	// The separation is what lets history be pruned without being forgotten.
	// A tool result is whatever the model read out of the project, so a record
	// that keeps every one verbatim and is never deleted accumulates exactly
	// the material nobody wants kept. Dropping the blob leaves the timeline,
	// the hash and one line saying what was there.
	//
	// A reader that finds no blob under Blob shows Summary in its place. That
	// is the design working, not a failure — Chronicle's own `get` returns an
	// Option for the same reason.
	Blob  string `json:"blob,omitempty"`
	Bytes int    `json:"bytes,omitempty"`
	// Summary is the payload's first line, capped. See payloadSummary.
	Summary string `json:"summary,omitempty"`

	// reasoning
	//
	// Its own record type rather than a field on the assistant message,
	// because the whole point is that it is *not* the answer. A scorer that
	// cannot tell them apart credits a model that worked the answer out and
	// then failed to give it.

	// turn
	//
	// Time is when the turn ended, RFC 3339. A durable record with no clock in
	// it is a poor one: the markdown the transcript used to hold carried the
	// turn's time in its header, and rebuilding that header is what reading
	// these rows back is for.
	Time      string  `json:"time,omitempty"`
	Outcome   string  `json:"outcome,omitempty"`
	Steps     int     `json:"steps,omitempty"`
	Sent      int     `json:"sent,omitempty"`
	Received  int     `json:"received,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
	CostKnown bool    `json:"cost_known,omitempty"`
	// TokensPerSecond is the turn's throughput, the same measurement the
	// closing usage line renders as "161 t/s". Absent when there is none —
	// nothing received, or too little elapsed time to divide by — which is why
	// it needs no companion flag: a real rate is never zero.
	//
	// Worth having beside the counts because throughput is a property of the
	// choice of model, not of the turn, and it is the one the token counts
	// cannot show. A sweep's sample size is capped by wall-clock as often as by
	// spend.
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	// Pinned lets a consumer distinguish files the model could see from files
	// it never saw when auditing whether a file was read before it was edited.
	Pinned []string `json:"pinned,omitempty"`
	// EditsExact and EditsFuzzy split the turn's applied edits by how the
	// text was found: verbatim, or by the line matcher guessing which lines
	// were meant. The split is the measurement behind whether that guessing
	// still earns its keep — see
	// doc/experiments/2026-09-anchored-edit/preregistration.md, M9.
	EditsExact int `json:"edits_exact,omitempty"`
	EditsFuzzy int `json:"edits_fuzzy,omitempty"`
	// Files is what the turn changed, root-relative — what Pinned and the edit
	// counts between them cannot say, since one is what the model could see and
	// the other is only how many edits landed.
	Files []string `json:"files,omitempty"`
	// Tools is the harness's own one-line summaries for the turn, in order:
	// "Read poll/poll.go (5 lines)", "‹check› lint $ golangci-lint run",
	// "failed (exit status 1)".
	//
	// On the turn rather than on each tool-result message, although the plan
	// this came from put it there. The set is not the same: toollog.go tees
	// Toolf, which the automatic checks and the commit also write to, and
	// neither of those is a tool the model called. Collecting per-message
	// summaries would quietly drop them, and they are the lines a later
	// session most wants — a check that failed and what was done about it is
	// carried by no diff. Phase 3's per-result summary, which exists so that
	// dropping a payload leaves its description behind, is a different field
	// for a different job.
	Tools []string `json:"tools,omitempty"`
	// OfferedTools names the tools the turn's model was given, and
	// EditFormat (shared with the session header) the mode they came from.
	// A session log once showed a model reporting that bash had vanished
	// mid-session, and nothing in the record could say whether it had — the
	// header gives only the mode at startup, and the requests are not kept.
	OfferedTools []string `json:"offered_tools,omitempty"`
	// Prompt and Answer are the turn as a reader sees it: the message that
	// opened it, and the answer with every interrupted send's content in
	// order and each steer as a blockquote.
	//
	// They overlap the message records, and that is the point. The arc is
	// assembled from live state that the messages do not carry — an
	// interrupted send's content is accumulated with its steer, while a failed
	// automatic check re-enters as a user message whose reply replaces what
	// came before. Rebuilding one from the other means encoding those rules a
	// second time, in a renderer, where a change to the first copy would break
	// the second silently. Recording the string the transcript was written
	// from is what makes the record and the screen agree by construction.
	Prompt string `json:"prompt,omitempty"`
	Answer string `json:"answer,omitempty"`

	// side_call
	//
	// The three requests Strument makes for itself — the commit message, the
	// session notes, the compaction summary — go out through the client
	// directly and so appear nowhere else in this log. They were invisible in
	// exactly the way that matters: a failed one shows up as a missing commit
	// message or a `/notes generate` that produced nothing, with no record of
	// which model was asked, how long it took, or what came back.
	//
	// Model, Outcome, Sent, Received, Cost and CostKnown are reused rather than
	// duplicated, since they mean here what they mean on a turn. Outcome's
	// vocabulary differs — "ok", "empty", "error", "deadline" — which the type
	// discriminator is what separates.

	// Call names which side request this was, in the same words the retry
	// messages use: "commit message", "session notes", "chat summary".
	Call string `json:"call,omitempty"`
	// Attempts counts requests actually sent, so a call that succeeded on its
	// third try is distinguishable from one that succeeded outright.
	Attempts int `json:"attempts,omitempty"`
	// Seconds is wall-clock for the whole call, retries and backoff included.
	// It is the measurement the budgets in side.go were set from, so a log that
	// omitted it would not let anyone check them against their own models.
	Seconds float64 `json:"seconds,omitempty"`
	// Error is the failure as the user was shown it, empty on success.
	Error string `json:"error,omitempty"`

	// decision
	//
	// One record per approve_model call, with Call, Model, Seconds, Outcome
	// ("approved", "asked", "failed"), Error and Cost as above. PSafe is what
	// the model answered; a pointer because 0 is an answer and absence (a
	// failed call) is not.
	PSafe *float64 `json:"p_safe,omitempty"`

	// rewind
	//
	// Rewound is how many turns a /rewind took out of the conversation. The
	// turns stay in the record; restore reads this row as a tombstone
	// (applyRewinds).
	Rewound int `json:"rewound,omitempty"`

	// request
	//
	// One record per request to the model, retries and continuations
	// included, where the turn record has only the sums. A sum cannot say
	// where a turn's cost went — fixed prompt, growing history, reasoning or
	// answer — and it is the per-request split that answers it: the
	// FrontierHarness run could say that output was 27-81% of a task's cost
	// and not which requests made it so.
	//
	// Call is "turn" or "aside"; side calls keep their own record above.
	// Model, Outcome, Sent, Received, Cost, CostKnown, Seconds and Error mean
	// what they do elsewhere, per request. Outcome is how the stream ended:
	// "done", "continuation", "context_exhausted", "output_exhausted",
	// "interrupted", "looping" or "failed".

	// Step is how many steps the turn had completed when the request went
	// out, counted as the turn record's Steps is: absent (0) for a turn's
	// first request and for an aside's. A retry repeats its step.
	Step int `json:"step,omitempty"`
	// FinishReason is the provider's own word for why the stream ended.
	FinishReason string `json:"finish_reason,omitempty"`
	// CacheRead and CacheWrite split Sent the way the provider reported it.
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
	// Reasoning is the part of Received the provider counted as reasoning.
	// Absent where the dialect has no split (Anthropic's).
	Reasoning int `json:"reasoning,omitempty"`
	// Provider is the upstream that served the request, where a router says
	// (OpenRouter). With fallbacks allowed it is the only record of which
	// provider a request went to, and providers differ in price, cache and
	// rate limit.
	Provider string `json:"provider,omitempty"`
}

// RecordToolCall is one call the model made, with its arguments verbatim —
// unless they were large enough to store separately, in which case they carry
// the same Blob/Bytes/Summary triple a message does and Arguments is empty.
//
// Arguments are stored separately for the same reasons results are, and the
// case that makes it worth doing is the same one toollog.go calls "the
// expensive half": a write call's arguments are the whole new file, and an
// edit call's are the original text being replaced — text that came out of a
// file that may hold something nobody wanted recorded.
type RecordToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Blob      string `json:"blob,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Summary   string `json:"summary,omitempty"`

	// Pruned is set when a restore found the arguments' blob gone, and says so
	// in words. Never written: it exists between reading a record and
	// rebuilding the conversation from it, where the note goes into this
	// call's result — Arguments itself has to stay JSON a provider accepts.
	Pruned string `json:"-"`
}

// Recorder receives the records. A nil Recorder on the Coder means no log,
// which is the default and costs nothing.
type Recorder interface {
	Record(r Record)
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
// callers write into different structs — a Record and a RecordToolCall — that
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

// SideCall is one finished side request, as sendSide saw it.
type SideCall struct {
	What     string
	Model    string
	Duration time.Duration
	Attempts int
	Outcome  string
	Err      string
	Usage    llm.Usage
}

// SideCallReporter receives a finished side call. Nil means no log.
//
// A function rather than a Recorder, and passed as a method value, for the
// same reason RecordTurnSideUsage is: the side callers are built in main.go
// before the Coder's Recorder is necessarily set, so a Recorder captured by
// value there would be the nil it had at construction.
type SideCallReporter func(SideCall)

// RecordSideCall logs one side request. It is the method value handed to
// CommitMessenger, NotesWriter and NewChatSummary.
func (c *Coder) RecordSideCall(s SideCall) {
	r := Record{
		Type:       "side_call",
		Call:       s.What,
		Model:      s.Model,
		Attempts:   s.Attempts,
		Seconds:    s.Duration.Round(time.Millisecond).Seconds(),
		Outcome:    s.Outcome,
		Error:      s.Err,
		Sent:       s.Usage.PromptTokens,
		Received:   s.Usage.CompletionTokens,
		CacheRead:  s.Usage.CacheReadTokens,
		CacheWrite: s.Usage.CacheWriteTokens,
		Reasoning:  s.Usage.ReasoningTokens,
		Provider:   s.Usage.Provider,
	}
	if s.Usage.Cost != nil {
		r.Cost, r.CostKnown = *s.Usage.Cost, true
	}
	c.record(r)
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
	usage llm.Usage, sawUsage bool, elapsed time.Duration,
) {
	if c.Recorder == nil {
		return
	}
	r := Record{
		Type:         "request",
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
		r := Record{Type: "message", Role: m.Role, ToolCallID: m.ToolCallID}
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
			rec := RecordToolCall{ID: tc.ID, Name: tc.Name}
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

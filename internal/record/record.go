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

package record

// Version is the schema version carried by the session record. Consumers
// live outside this repository, so a change to a field's meaning needs a number
// they can branch on.
const Version = 1

// Record is one line of the JSONL log. It is deliberately one flat struct with
// a Type discriminator rather than a family of types: the consumer is usually
// three lines of jq or Python, and a flat shape keeps `select(.type=="message")`
// the whole of the parsing.
type Record struct {
	Type string `json:"type"`

	// session
	Version int    `json:"version,omitempty"`
	Model   string `json:"model,omitempty"`
	Root    string `json:"root,omitempty"`
	// Slug is a turn row's model without its provider: see Model.Slug in
	// markModelSeams for why that is the comparison. Rows written before it
	// existed carry only Model.
	Slug string `json:"slug,omitempty"`

	// message
	Role string `json:"role,omitempty"`
	Text string `json:"text,omitempty"`
	// ToolCalls carries the arguments as the model sent them, which the
	// rendered stream shows only as a one-line summary. A scorer counting
	// "did it search for X" needs the argument, not the summary — one of the
	// eleven counted "FINISHED" as a result when it was in the command string.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID is set on a tool-result message and matches the call it
	// answers, so results can be paired with arguments without guessing.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Path is the file an "edit" record's call targeted.
	Path string `json:"path,omitempty"`

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
	//
	// Field is the wire field it streamed on ("reasoning" or
	// "reasoning_content"), empty when the dialect names none. A restore
	// needs it to replay the reasoning to a model with replay_reasoning,
	// which sends it back on the field it came on.
	Field string `json:"field,omitempty"`

	// turn
	//
	// Time is when the turn ended, RFC 3339. A durable record with no clock in
	// it is a poor one: the markdown the transcript used to hold carried the
	// turn's time in its header, and rebuilding that header is what reading
	// these rows back is for.
	Time    string `json:"time,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Steps   int    `json:"steps,omitempty"`
	Sent    int    `json:"sent,omitempty"`
	// Estimated is Strument's own estimate of a request whose provider count
	// is Sent. The pair is what a restore learns the token ratio from
	// (calibrateTokens), before the restored history is measured.
	Estimated int     `json:"estimated,omitempty"`
	Received  int     `json:"received,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
	CostKnown bool    `json:"cost_known,omitempty"`
	// GoAllowance is what opencode Go charged the turn against its
	// subscription, in dollars, and GoListCost the same requests at list
	// price; GoKnown says the turn was charged to Go at all, so that a free
	// model's zero is not read as an absence. Cost is 0 on these turns.
	GoAllowance float64 `json:"go_allowance,omitempty"`
	GoListCost  float64 `json:"go_list_cost,omitempty"`
	GoKnown     bool    `json:"go_known,omitempty"`
	// GoMeter is the subscription's meter as the turn ended, when it was read.
	GoMeter *GoMeter `json:"go_meter,omitempty"`
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
	// OfferedTools names the tools the turn's model was given. A session log
	// once showed a model reporting that bash had vanished mid-session, and
	// nothing in the record could say whether it had — the requests are not
	// kept. (Sessions from before /ask was removed also carry an edit_format
	// here and in the header; nothing reads it.)
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

	// compaction
	//
	// One record per fold of the history, with Text set to what replaced the
	// folded turns: the summary, or the roll note. Before this, a summary was
	// visible only to the model that read it, so a wrong answer drawn from a
	// summary could not be traced to it
	// (doc/experiments/2026-10-recall-models). Mode is "summary" or "roll";
	// Outcome is "ok", or "not_smaller" for a summary that was refused, whose
	// text is kept too because the refusal is what needs explaining.
	// TokensBefore and TokensAfter are the history's estimated size.
	Mode         string `json:"mode,omitempty"`
	TokensBefore int    `json:"tokens_before,omitempty"`
	TokensAfter  int    `json:"tokens_after,omitempty"`

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

// ToolCall is one call the model made, with its arguments verbatim —
// unless they were large enough to store separately, in which case they carry
// the same Blob/Bytes/Summary triple a message does and Arguments is empty.
//
// Arguments are stored separately for the same reasons results are, and the
// case that makes it worth doing is the same one toollog.go calls "the
// expensive half": a write call's arguments are the whole new file, and an
// edit call's are the original text being replaced — text that came out of a
// file that may hold something nobody wanted recorded.
type ToolCall struct {
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

// Recorder receives the records. A nil Recorder on a coder.Coder means no
// log, which is the default and costs nothing.
type Recorder interface {
	Record(r Record)
}

// The kinds of record, the value of Record.Type.
const (
	KindSession    = "session"    // a run of the session began
	KindModel      = "model"      // the user switched models
	KindMessage    = "message"    // one message of the conversation
	KindReasoning  = "reasoning"  // reasoning, kept apart from the answer
	KindTurn       = "turn"       // a turn ended; it accounts for the messages before it
	KindRequest    = "request"    // one request to a model
	KindRewind     = "rewind"     // /rewind took turns out
	KindCompaction = "compaction" // the history was compacted
	KindDecision   = "decision"   // the approval model answered a prompt
	KindEdit       = "edit"       // an edit call's outcome
)

// OutcomeCrashed is the turn outcome for a turn that died with a panic. It is
// not a coder.SendOutcome: no send produced it, and by the time it is known
// the sends are over.
const OutcomeCrashed = "Crashed"

// GoMeter is opencode Go's three spending windows, as read from its usage
// endpoint: whole percentages of each window's limit.
type GoMeter struct {
	Rolling GoWindow `json:"rolling"`
	Weekly  GoWindow `json:"weekly"`
	Monthly GoWindow `json:"monthly"`
}

// GoWindow is one window of a GoMeter.
type GoWindow struct {
	Percent  float64 `json:"percent"`
	Limited  bool    `json:"limited,omitempty"`
	ResetsAt string  `json:"resets_at,omitempty"`
}

// Rebuilding a conversation from the record, and the repairs that keep the
// result sendable.

package coder

import (
	"encoding/json"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// msg is a message row as the recorder writes one.
func msg(role, text string) Record {
	return Record{Type: "message", Role: role, Text: text}
}

func callRow(text string, calls ...RecordToolCall) Record {
	return Record{Type: "message", Role: llm.RoleAssistant, Text: text, ToolCalls: calls}
}

func resultRow(id, text string) Record {
	return Record{Type: "message", Role: llm.RoleTool, ToolCallID: id, Text: text}
}

// shape renders a conversation compactly, so a test can say what it wants
// without a wall of struct literals.
func shape(msgs []llm.Message) string {
	var parts []string
	for _, m := range msgs {
		var p strings.Builder
		p.WriteString(m.Role)
		for _, tc := range m.ToolCalls {
			p.WriteString("+" + tc.Name)
		}
		if m.ToolCallID != "" {
			p.WriteString("(" + m.ToolCallID + ")")
		}
		parts = append(parts, p.String())
	}
	return strings.Join(parts, " ")
}

// An ordinary session comes back as itself.
func TestMessagesFromRecordsRebuildsAnOrdinaryConversation(t *testing.T) {
	records := []Record{
		{Type: "session", Version: RecordVersion, Model: "flash"},
		msg(llm.RoleUser, "what is in main.go?"),
		{Type: "reasoning", Text: "I should read it first."},
		callRow("Let me look.", RecordToolCall{ID: "c1", Name: "read", Arguments: `{"path":"main.go"}`}),
		resultRow("c1", "main.go (3 lines)\n1\tpackage main\n"),
		msg(llm.RoleAssistant, "An empty main function."),
		{Type: "turn", Model: "openrouter/flash", Outcome: "Success", Prompt: "what is in main.go?"},
	}

	got, stats := MessagesFromRecords(records)

	if want := "user assistant+read tool(c1) assistant"; shape(got) != want {
		t.Errorf("shape = %q, want %q", shape(got), want)
	}
	if stats.Dropped != 0 {
		t.Errorf("dropped %d messages from a clean record", stats.Dropped)
	}
	if got[1].ToolCalls[0].Arguments != `{"path":"main.go"}` {
		t.Errorf("the call's arguments did not survive: %q", got[1].ToolCalls[0].Arguments)
	}
	// Reasoning is recorded but never sent back.
	for _, m := range got {
		if strings.Contains(m.Text(), "I should read it first") {
			t.Error("reasoning was folded back into the conversation")
		}
	}
	if len(stats.Models) != 1 || stats.Models[0] != "openrouter/flash" {
		t.Errorf("models = %v, want the one the turn row named", stats.Models)
	}
}

// The repair that matters most. An unanswered tool call does not cost one
// result: it makes every subsequent request malformed, so the session ends on
// its first send rather than degrading.
func TestAnUnansweredToolCallIsNotRestored(t *testing.T) {
	records := []Record{
		msg(llm.RoleUser, "read both"),
		callRow("Reading.",
			RecordToolCall{ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
			RecordToolCall{ID: "c2", Name: "read", Arguments: `{"path":"b"}`}),
		resultRow("c1", "a (1 line)"),
		// c2 never answered: the process died between the ask and the dispatch.
	}

	got, stats := MessagesFromRecords(records)

	if want := "user assistant+read tool(c1)"; shape(got) != want {
		t.Errorf("shape = %q, want %q", shape(got), want)
	}
	if stats.Dropped != 1 {
		t.Errorf("dropped = %d, want the one unanswered call", stats.Dropped)
	}
	assertSendable(t, got)
}

// An assistant message whose every call went unanswered, and which said
// nothing besides, is the shell of a turn rather than a turn.
func TestAnAssistantMessageWithNothingLeftIsDropped(t *testing.T) {
	records := []Record{
		msg(llm.RoleUser, "go"),
		callRow("", RecordToolCall{ID: "c1", Name: "read", Arguments: `{}`}),
	}
	got, stats := MessagesFromRecords(records)
	if want := "user"; shape(got) != want {
		t.Errorf("shape = %q, want %q", shape(got), want)
	}
	if stats.Dropped == 0 {
		t.Error("the empty assistant message was not counted as dropped")
	}
}

// Its text survives when it had some: the model said something, and only the
// call it made is gone.
func TestAnAssistantMessageKeepsItsTextWhenItsCallsGo(t *testing.T) {
	records := []Record{
		msg(llm.RoleUser, "go"),
		callRow("Let me check that.", RecordToolCall{ID: "c1", Name: "read", Arguments: `{}`}),
	}
	got, _ := MessagesFromRecords(records)
	if want := "user assistant"; shape(got) != want {
		t.Fatalf("shape = %q, want %q", shape(got), want)
	}
	if got[1].Text() != "Let me check that." {
		t.Errorf("text = %q, want the model's own words", got[1].Text())
	}
}

// A tool result with no call ahead of it carries a tool_call_id no request
// contains, which is as malformed as the other direction.
func TestAnOrphanToolResultIsNotRestored(t *testing.T) {
	records := []Record{
		msg(llm.RoleUser, "go"),
		resultRow("c9", "from a call that is not in this record"),
		msg(llm.RoleAssistant, "Done."),
	}
	got, stats := MessagesFromRecords(records)
	if want := "user assistant"; shape(got) != want {
		t.Errorf("shape = %q, want %q", shape(got), want)
	}
	if stats.Dropped != 1 {
		t.Errorf("dropped = %d, want the one orphan", stats.Dropped)
	}
	assertSendable(t, got)
}

// A system message mid-conversation is legal only directly after a user turn.
// Anthropic rejects one that follows an assistant turn outright, so restoring
// it anywhere else would end the session on its first request.
func TestASystemMessageIsRestoredOnlyAfterAUserTurn(t *testing.T) {
	legal := []Record{
		msg(llm.RoleUser, "go"),
		msg(llm.RoleSystem, "No reply came back: the request exceeded the context limit."),
	}
	if got, _ := MessagesFromRecords(legal); shape(got) != "user system" {
		t.Errorf("a system note after a user turn was dropped: %q", shape(got))
	}

	illegal := []Record{
		msg(llm.RoleUser, "go"),
		msg(llm.RoleAssistant, "Done."),
		msg(llm.RoleSystem, "something no provider would take here"),
	}
	got, stats := MessagesFromRecords(illegal)
	if shape(got) != "user assistant" {
		t.Errorf("a system message after an assistant turn was restored: %q", shape(got))
	}
	if stats.Dropped != 1 {
		t.Errorf("dropped = %d, want the one illegal system message", stats.Dropped)
	}
}

// `strument history edit` exists so that something which should not have been
// recorded can be taken out. Taking out the first prompt leaves the reply to
// it at the front, and a conversation does not start there.
func TestRecordsBeforeTheFirstUserTurnAreDropped(t *testing.T) {
	records := []Record{
		msg(llm.RoleAssistant, "a reply whose prompt was edited out"),
		msg(llm.RoleUser, "the first prompt left in the file"),
		msg(llm.RoleAssistant, "Done."),
	}
	got, stats := MessagesFromRecords(records)
	if want := "user assistant"; shape(got) != want {
		t.Errorf("shape = %q, want %q", shape(got), want)
	}
	if stats.Dropped != 1 {
		t.Errorf("dropped = %d, want the orphaned reply", stats.Dropped)
	}
}

// assertSendable checks the two invariants a provider enforces, which are the
// ones this whole file exists to maintain.
func assertSendable(t *testing.T, msgs []llm.Message) {
	t.Helper()
	// Counted, not a set. A set says "some result carries this id", which is
	// true of two calls sharing one id and answered once — the exact defect
	// this file's repair exists for, and one a set-membership check cannot
	// see.
	answered := map[string]int{}
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			answered[m.ToolCallID]++
		}
	}
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			if answered[tc.ID] == 0 {
				t.Errorf("message %d asks for tool call %q with no result: %s", i, tc.ID, shape(msgs))
				continue
			}
			answered[tc.ID]--
		}
		if m.Role == llm.RoleSystem && i > 0 && msgs[i-1].Role != llm.RoleUser {
			t.Errorf("message %d is a system message after a %s turn: %s", i, msgs[i-1].Role, shape(msgs))
		}
	}
}

// The seam. A conversation made by another model gets one; a conversation made
// by this one does not, which is the half that keeps the check honest.
func TestTheSeamNoteFiresOnlyForAnotherModel(t *testing.T) {
	c := testCoder(t)
	now := c.Model.QualifiedSlug()

	if c.RestoredFromAnotherModel(RestoreStats{Models: []string{now}}) {
		t.Error("a conversation this model made was reported as another model's")
	}
	if !c.RestoredFromAnotherModel(RestoreStats{Models: []string{"some/other-model"}}) {
		t.Error("a conversation another model made was not noticed")
	}
	// A session whose model was switched mid-way has several, and one of them
	// differing is enough: some of those assistant turns are not this model's.
	if !c.RestoredFromAnotherModel(RestoreStats{Models: []string{now, "some/other-model"}}) {
		t.Error("a session that switched models mid-way was not noticed")
	}
	// Nothing recorded, nothing to claim.
	if c.RestoredFromAnotherModel(RestoreStats{}) {
		t.Error("a restore with no recorded model asserted one anyway")
	}
}

// An interrupted turn's row still names its model, but the turn left no
// assistant message to attribute — the reply was cut off before the model
// recorded anything, and the restore carries nothing of it. A seam note about
// turns the conversation does not contain is the note misfiring, and it fired
// on exactly this: a session reopened once under the default model,
// interrupted, then resumed under its own model for the rest of its life.
func TestAnInterruptedTurnWithNoAnswerLeavesNoSeam(t *testing.T) {
	records := []Record{
		{Type: "session", Version: RecordVersion, Model: "mimo"},
		// The prompt is a blob and the harness note is all that followed:
		// the model never emitted a message before the interrupt.
		msg(llm.RoleUser, ""),
		msg(llm.RoleUser, "[strument] The user pressed Ctrl-C, so your reply above was cut off."),
		{Type: "turn", Model: "openrouter/xiaomi/mimo-v2.5", Outcome: "Interrupted", Prompt: "diagnose the run_code failure"},
	}

	got, stats := MessagesFromRecords(records)

	if want := strings.Join([]string{llm.RoleUser, llm.RoleUser}, " "); shape(got) != want {
		t.Errorf("shape = %q, want %q: the interrupted turn restored with nothing to show for it", shape(got), want)
	}
	if len(stats.Models) != 0 {
		t.Errorf("models = %v; a turn with no surviving assistant message attributes none", stats.Models)
	}
}

func TestTheSeamNoteIsAMarkedUserTurn(t *testing.T) {
	c := testCoder(t)
	c.RestoreHistory([]llm.Message{llm.TextMessage(llm.RoleUser, "hi")}, nil)
	c.NoteRestoredFromAnotherModel()

	last := c.doneMessages[len(c.doneMessages)-1]
	// Never an assistant turn: putting words in the model's mouth is the one
	// thing no provider will catch for us. Never system: this lands after an
	// assistant turn, which is where Anthropic rejects one.
	if last.Role != llm.RoleUser {
		t.Errorf("the seam note is a %s message, want a marked user turn", last.Role)
	}
	if !strings.HasPrefix(last.Text(), llm.HarnessMarker) {
		t.Errorf("the seam note is not marked as the harness speaking: %q", last.Text())
	}
	if !strings.Contains(last.Text(), "a different model") {
		t.Errorf("the seam note does not say what it is for: %q", last.Text())
	}
}

// Restoring puts the conversation in settled history, not in the turn in
// progress. The recorder's watermark walks curMessages, so a conversation
// seeded there would be written into this run's segment as though it had just
// happened — duplicating the whole history on every resume.
func TestRestoreDoesNotPutTheConversationInTheCurrentTurn(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.RestoreHistory([]llm.Message{
		llm.TextMessage(llm.RoleUser, "an earlier prompt"),
		llm.TextMessage(llm.RoleAssistant, "an earlier answer"),
	}, nil)

	if len(c.curMessages) != 0 {
		t.Errorf("restore left %d messages in the current turn", len(c.curMessages))
	}
	if len(c.doneMessages) != 2 {
		t.Errorf("settled history has %d messages, want the 2 restored", len(c.doneMessages))
	}
	c.recordNewMessages()
	for _, r := range rec.recs {
		if strings.Contains(r.Text, "an earlier") {
			t.Errorf("a restored message was written back into this run's record: %+v", r)
		}
	}
}

func TestRestoreNoteSaysWhatCameBack(t *testing.T) {
	if got := (RestoreStats{}).RestoreNote(); got != "" {
		t.Errorf("an empty restore said %q, want nothing", got)
	}
	if got := (RestoreStats{Messages: 1}).RestoreNote(); !strings.Contains(got, "1 message from") {
		t.Errorf("one message should be singular: %q", got)
	}
	got := RestoreStats{Messages: 12, Dropped: 2}.RestoreNote()
	for _, want := range []string{"12 messages", "2 could not be restored"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note does not mention %q: %q", want, got)
		}
	}
}

// Tool call ids repeat, and matching them across the whole record is wrong.
//
// A provider need only make an id unique within one request. The record spans
// every request a session ever made, so a global match lets a later result
// answer an earlier call of the same name — restoring an unanswered call as
// though it were fine and making the next request malformed. This was a real
// bug, found by running a restore against a stub that reuses one id, which is
// the cheap version of a provider that happens to.
func TestARepeatedToolCallIDDoesNotAnswerAnEarlierCall(t *testing.T) {
	records := []Record{
		msg(llm.RoleUser, "first prompt"),
		// The process died here: this call never got a result.
		callRow("Reading.", RecordToolCall{ID: "call_1", Name: "read", Arguments: `{"path":"a"}`}),

		msg(llm.RoleUser, "second prompt, a later run"),
		callRow("Reading again.", RecordToolCall{ID: "call_1", Name: "read", Arguments: `{"path":"b"}`}),
		resultRow("call_1", "b (1 line)"),
		msg(llm.RoleAssistant, "Done."),
	}

	got, stats := MessagesFromRecords(records)

	want := "user assistant user assistant+read tool(call_1) assistant"
	if shape(got) != want {
		t.Errorf("shape = %q, want %q", shape(got), want)
	}
	// The first message keeps its text and loses its call; the second keeps
	// both, because its result really does follow it.
	if len(got[1].ToolCalls) != 0 {
		t.Errorf("an unanswered call was restored because a later call shared its id: %+v", got[1].ToolCalls)
	}
	if len(got[3].ToolCalls) != 1 {
		t.Errorf("the answered call was dropped: %+v", got[3].ToolCalls)
	}
	if stats.Dropped != 1 {
		t.Errorf("dropped = %d, want the one unanswered call", stats.Dropped)
	}
	assertSendable(t, got)
}

// One result cannot answer two calls, for the same reason.
func TestOneResultAnswersOneCall(t *testing.T) {
	records := []Record{
		msg(llm.RoleUser, "go"),
		callRow("Reading both.",
			RecordToolCall{ID: "call_1", Name: "read", Arguments: `{"path":"a"}`},
			RecordToolCall{ID: "call_1", Name: "read", Arguments: `{"path":"b"}`}),
		resultRow("call_1", "a (1 line)"),
	}
	got, _ := MessagesFromRecords(records)
	if n := len(got[1].ToolCalls); n != 1 {
		t.Errorf("%d calls restored against one result, want 1", n)
	}
	assertSendable(t, got)
}

// The note for pruned arguments reaches the model in front of the result of
// the call it belongs to, and the call itself stays valid JSON.
func TestRestoredCallWithPrunedArgumentsIsLabelled(t *testing.T) {
	note := "[strument] This call's arguments are no longer stored. They were 3031 bytes."
	msgs, stats := MessagesFromRecords([]Record{
		{Type: "message", Role: llm.RoleUser, Text: "write it"},
		{Type: "message", Role: llm.RoleAssistant, ToolCalls: []RecordToolCall{
			{ID: "c1", Name: "write", Arguments: "{}", Pruned: note},
			{ID: "c2", Name: "read", Arguments: `{"path":"a"}`},
		}},
		{Type: "message", Role: llm.RoleTool, ToolCallID: "c1", Text: "Created big.txt."},
		{Type: "message", Role: llm.RoleTool, ToolCallID: "c2", Text: "a (1 line)"},
	})
	if stats.Dropped != 0 || len(msgs) != 4 {
		t.Fatalf("msgs = %d, dropped = %d", len(msgs), stats.Dropped)
	}
	if args := msgs[1].ToolCalls[0].Arguments; !json.Valid([]byte(args)) {
		t.Errorf("restored arguments %q are not JSON; a provider refuses the request", args)
	}
	if got := msgs[2].Text(); got != note+"\n\nCreated big.txt." {
		t.Errorf("c1's result = %q, want the note in front of it", got)
	}
	if got := msgs[3].Text(); got != "a (1 line)" {
		t.Errorf("c2's result = %q; a call that kept its arguments gets no note", got)
	}
}

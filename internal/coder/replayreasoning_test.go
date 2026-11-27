package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/record"
)

// reasoned is a turn that thinks on field before answering.
func reasoned(thought, field, text string) fixture.Turn {
	return fixture.Turn{Events: []fixture.Event{
		{Kind: string(llm.EventReasoning), Text: thought, Field: field},
		{Kind: string(llm.EventAnswer), Text: text},
	}}
}

// runTwo runs two turns, the first of which reasons, and returns the requests.
func runTwo(t *testing.T, replay bool, field string) (*Coder, []llm.Request) {
	t.Helper()
	c := testCoder(t)
	c.Model.ReplayReasoning = replay
	var reqs []llm.Request
	c.Client = &fixture.StreamStub{
		Turns: []fixture.Turn{reasoned("the map so far: A east of B", field, "first"), answer("second")},
		OnRequest: func(_ int, req llm.Request, _ *fixture.Request) error {
			reqs = append(reqs, req)
			return nil
		},
	}
	c.Run(context.Background(), "one")
	c.Run(context.Background(), "two")
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2", len(reqs))
	}
	return c, reqs
}

// The reasoning a reply streamed is kept on its message, with the field, and
// the next request asks for replay exactly when the model is set to.
func TestReplayReasoningReachesTheNextRequest(t *testing.T) {
	for _, replay := range []bool{false, true} {
		_, reqs := runTwo(t, replay, llm.ReasoningFieldContent)
		if reqs[1].ReplayReasoning != replay {
			t.Errorf("replay %v: request says %v", replay, reqs[1].ReplayReasoning)
		}
		var found bool
		for _, m := range reqs[1].Messages {
			if m.Role == llm.RoleAssistant && m.Text() == "first" {
				found = true
				if m.Reasoning != "the map so far: A east of B" || m.ReasoningField != llm.ReasoningFieldContent {
					t.Errorf("replay %v: assistant message kept %q on %q", replay, m.Reasoning, m.ReasoningField)
				}
			}
		}
		if !found {
			t.Fatalf("replay %v: the first answer is not in the second request", replay)
		}
	}
}

// The token estimate counts replayed reasoning, and only that: with replay off
// the reasoning is not sent, so it takes no room.
func TestReplayReasoningIsCounted(t *testing.T) {
	c, _ := runTwo(t, true, llm.ReasoningFieldReasoning)
	on := c.countMessages(c.doneMessages)
	c.Model.ReplayReasoning = false
	off := c.countMessages(c.doneMessages)
	want := c.Tokens.Count("the map so far: A east of B")
	if on-off != want {
		t.Errorf("replay adds %d tokens to the estimate, want %d", on-off, want)
	}
	if got := c.historyTokens(c.doneMessages); got != off {
		t.Errorf("history budget with replay off = %d, want %d", got, off)
	}
}

// Old reasoning goes when old tool results do, and the protected tail keeps
// its own.
func TestPruneClearsOldReasoning(t *testing.T) {
	c := testCoder(t)
	c.Model.ReplayReasoning = true
	var msgs []llm.Message
	for _, id := range []string{"a", "b", "c", "d"} {
		turn := toolTurn(id, 1000)
		turn[1].Model = c.Model.Slug
		turn[1].Reasoning = strings.Repeat("y", 2000)
		turn[1].ReasoningField = llm.ReasoningFieldReasoning
		msgs = append(msgs, turn...)
	}

	n, r, _ := pruneToolResults(msgs, 4000, c.msgTokens, clearedToolResult)

	if n == 0 || r == 0 {
		t.Fatalf("cleared %d results and %d replies' reasoning", n, r)
	}
	if msgs[1].Reasoning != "" {
		t.Error("the oldest reply kept its reasoning")
	}
	if msgs[len(msgs)-3].Reasoning == "" {
		t.Error("the newest reply, inside the protected tail, lost its reasoning")
	}
}

// With replay off the reasoning is not sent, so clearing it frees nothing and
// it is left alone.
func TestPruneLeavesUnsentReasoning(t *testing.T) {
	c := testCoder(t)
	var msgs []llm.Message
	for _, id := range []string{"a", "b", "c", "d"} {
		turn := toolTurn(id, 1000)
		turn[1].Model = c.Model.Slug
		turn[1].Reasoning = "kept"
		turn[1].ReasoningField = llm.ReasoningFieldReasoning
		msgs = append(msgs, turn...)
	}
	if _, r, _ := pruneToolResults(msgs, 3000, c.msgTokens, clearedToolResult); r != 0 {
		t.Errorf("cleared %d replies' reasoning that no request sends", r)
	}
}

// A resumed session replays as the live one did: each reasoning row goes back
// on the assistant message it preceded, with its field, including the second
// step of a turn that called a tool in between.
func TestReplayReasoningSurvivesARestore(t *testing.T) {
	c := testCoder(t)
	c.Model.ReplayReasoning = true
	if err := os.WriteFile(filepath.Join(c.Root, "notes.txt"), []byte("the code is 4719\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &capture{}
	c.Recorder = rec
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: string(llm.EventReasoning), Text: "plan: read the notes", Field: llm.ReasoningFieldContent},
			{Kind: string(llm.EventToolCall), ToolIndex: 0, ToolID: "call_1", ToolName: "read", ToolArgs: `{"path": "notes.txt"}`},
		}},
		reasoned("the notes say 4719", llm.ReasoningFieldContent, "4719"),
	}}
	c.Run(context.Background(), "What is the code?")

	restored, stats := MessagesFromRecords(rec.recs)
	var live, back []string
	for _, m := range c.doneMessages {
		live = append(live, m.Role+":"+m.Reasoning+":"+m.ReasoningField)
	}
	for _, m := range restored {
		back = append(back, m.Role+":"+m.Reasoning+":"+m.ReasoningField)
	}
	if strings.Join(back, "|") != strings.Join(live, "|") {
		t.Fatalf("restored reasoning %q, live %q", back, live)
	}
	if !strings.Contains(strings.Join(live, "|"), "plan: read the notes") || !strings.Contains(strings.Join(live, "|"), "the notes say 4719") {
		t.Fatalf("the live history is missing a step's reasoning: %q", live)
	}

	d := testCoder(t)
	d.Model.ReplayReasoning = true
	d.RestoreHistory(restored, stats.Turns)
	var req llm.Request
	d.Client = &fixture.StreamStub{
		Turns: []fixture.Turn{answer("again")},
		OnRequest: func(_ int, r llm.Request, _ *fixture.Request) error {
			req = r
			return nil
		},
	}
	d.Run(context.Background(), "And again?")
	var sent []string
	for _, m := range req.Messages {
		if r := m.ReplayedReasoning(req.Model); r != "" {
			sent = append(sent, r)
		}
	}
	if strings.Join(sent, "|") != "plan: read the notes|the notes say 4719" {
		t.Errorf("after a restore the request replays %q", sent)
	}
}

// A reasoning row from a send that produced no message does not attach to the
// next assistant message, which a later send wrote with reasoning of its own.
func TestAnOrphanReasoningRowIsNotRestored(t *testing.T) {
	recs := []record.Record{
		{Type: record.KindMessage, Role: llm.RoleUser, Text: "one"},
		{Type: record.KindReasoning, Text: "interrupted thought", Field: llm.ReasoningFieldReasoning},
		{Type: record.KindTurn, Model: "m"},
		{Type: record.KindMessage, Role: llm.RoleUser, Text: "two"},
		{Type: record.KindMessage, Role: llm.RoleAssistant, Text: "answer"},
		{Type: record.KindTurn, Model: "m"},
	}
	msgs, _ := MessagesFromRecords(recs)
	for _, m := range msgs {
		if m.Reasoning != "" {
			t.Errorf("%s message %q took reasoning %q", m.Role, m.Text(), m.Reasoning)
		}
	}
}

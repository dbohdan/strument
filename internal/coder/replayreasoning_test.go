package coder

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
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

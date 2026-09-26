package coder

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

func answer(text string) fixture.Turn {
	return fixture.Turn{Events: []fixture.Event{{Kind: "Answer", Text: text}}}
}

func texts(msgs []llm.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role + ":" + m.Text()
	}
	return out
}

// TestRewindAgreesWithRestore is the property the design rests on: after any
// sequence of turns and rewinds, the live history equals what a restore
// rebuilds from the record, tombstones and all. If they ever disagree, a
// restart changes the conversation.
func TestRewindAgreesWithRestore(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{answer("A1"), answer("A2"), answer("A3"), answer("A4")}}
	ctx := context.Background()
	check := func(step string, want []string) {
		t.Helper()
		live := texts(c.doneMessages)
		restored, _ := MessagesFromRecords(rec.recs)
		if !slices.Equal(live, want) {
			t.Errorf("%s: live history %q, want %q", step, live, want)
		}
		if got := texts(restored); !slices.Equal(got, live) {
			t.Errorf("%s: restore rebuilds %q, but the live history is %q", step, got, live)
		}
	}

	for _, q := range []string{"Q1", "Q2", "Q3"} {
		c.Run(ctx, q)
	}
	if _, err := c.Rewind(1); err != nil {
		t.Fatal(err)
	}
	check("rewind 1 of 3", []string{"user:Q1", "assistant:A1", "user:Q2", "assistant:A2"})

	c.Run(ctx, "Q4")
	if _, err := c.Rewind(2); err != nil {
		t.Fatal(err)
	}
	check("rewind 2 after a new turn", []string{"user:Q1", "assistant:A1"})

	if _, err := c.Rewind(2); err == nil || !strings.Contains(err.Error(), "only 1 turn") {
		t.Errorf("rewinding past the start: err = %v, want a refusal naming 1 turn", err)
	}
	check("a refused rewind changes nothing", []string{"user:Q1", "assistant:A1"})
}

// TestRewindKeepsToolCallsPaired: whole turns come out, so a surviving turn's
// calls and results stay together, and a rewound turn's model is no longer
// attributed.
func TestRewindKeepsToolCallsPaired(t *testing.T) {
	records := []Record{
		{Type: "message", Role: llm.RoleUser, Text: "look"},
		{Type: "message", Role: llm.RoleAssistant, ToolCalls: []RecordToolCall{{ID: "x", Name: "read", Arguments: `{"path":"a"}`}}},
		{Type: "message", Role: llm.RoleTool, ToolCallID: "x", Text: "contents"},
		{Type: "message", Role: llm.RoleAssistant, Text: "seen"},
		{Type: "turn", Model: "mimo", Files: nil},
		{Type: "message", Role: llm.RoleUser, Text: "now edit"},
		{Type: "message", Role: llm.RoleAssistant, Text: "edited"},
		{Type: "turn", Model: "glm", Files: []string{"a"}},
		{Type: "rewind", Rewound: 1},
	}
	got, stats := MessagesFromRecords(records)
	if want := []string{"user:look", "assistant:", "tool:contents", "assistant:seen"}; !slices.Equal(texts(got), want) {
		t.Errorf("messages %q, want %q", texts(got), want)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Errorf("the surviving call lost its pairing: %+v", got[1])
	}
	if !slices.Equal(stats.Models, []string{"mimo"}) {
		t.Errorf("models %q; a rewound turn's model must not be attributed", stats.Models)
	}
	if stats.Rewound != 1 || len(stats.Turns) != 1 || stats.Turns[0].Start != 0 || stats.Turns[0].End != 4 {
		t.Errorf("rewound %d, turns %+v", stats.Rewound, stats.Turns)
	}
}

// TestRewindReportsFilesAndRefusesPastCompaction: the files the rewound turns
// changed come back for the user's line, and turns a summary absorbed cannot
// be taken out.
func TestRewindReportsFilesAndRefusesPastCompaction(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "q1"), llm.TextMessage(llm.RoleAssistant, "a1"),
		llm.TextMessage(llm.RoleUser, "q2"), llm.TextMessage(llm.RoleAssistant, "a2"),
	}
	c.turns = []TurnSpan{{0, 2, []string{"b.go"}}, {2, 4, []string{"a.go", "b.go"}}}
	res, err := c.Rewind(2)
	if err != nil || res.Turns != 2 || !slices.Equal(res.Files, []string{"a.go", "b.go"}) {
		t.Errorf("res %+v err %v; want 2 turns and a.go, b.go", res, err)
	}
	if _, err := c.Rewind(1); !errors.Is(err, errNothingToRewind) {
		t.Errorf("empty: err = %v", err)
	}
	c.compactedTurns = true
	if _, err := c.Rewind(1); err == nil || !strings.Contains(err.Error(), "compacted") {
		t.Errorf("after compaction: err = %v, want the compaction named", err)
	}
}

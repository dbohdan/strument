package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// toolTurn is one turn that read a file: the request, the call, a result of
// `tokens` tokens, and the answer.
func toolTurn(id string, tokens int) []llm.Message {
	return []llm.Message{
		llm.TextMessage("user", "Note for later: the build cache prefix is ochre-v12. Read "+id+"."),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", Arguments: `{"path":"` + id + `.go"}`}}},
		llm.ToolResult(id, strings.Repeat("x", tokens*4)),
		llm.TextMessage("assistant", "It parses ranges."),
	}
}

func TestPruneClearsOldResultsAndKeepsTheRest(t *testing.T) {
	var msgs []llm.Message
	for _, id := range []string{"a", "b", "c", "d"} {
		msgs = append(msgs, toolTurn(id, 1000)...)
	}
	count := func(m llm.Message) int { return (&ChatSummary{tokens: RuneCounter{}}).count(m) }

	n, freed := pruneToolResults(msgs, 3000, count)

	if n == 0 || freed < 3000/8 {
		t.Fatalf("cleared %d results, freed %d tokens", n, freed)
	}
	if !strings.HasPrefix(msgs[2].Text(), clearedToolResult) {
		t.Error("the oldest result was not cleared")
	}
	if strings.HasPrefix(msgs[len(msgs)-2].Text(), clearedToolResult) {
		t.Error("the newest result, inside the protected tail, was cleared")
	}
	for i, m := range msgs {
		switch {
		case m.Role == llm.RoleUser && !strings.Contains(m.Text(), "ochre-v12"):
			t.Errorf("message %d: a user message changed: %q", i, m.Text())
		case m.Role == llm.RoleAssistant && len(m.ToolCalls) == 1 && m.ToolCalls[0].Arguments == "":
			t.Errorf("message %d: a call lost its arguments", i)
		case m.Role == llm.RoleTool && m.ToolCallID == "":
			t.Errorf("message %d: a result lost its call id", i)
		}
	}
}

// Clearing rewrites the cached prefix as a fold does, so a small gain is not
// worth it.
func TestPruneSkipsASmallGain(t *testing.T) {
	var msgs []llm.Message
	for _, id := range []string{"a", "b", "c"} {
		msgs = append(msgs, toolTurn(id, 60)...)
	}
	count := func(m llm.Message) int { return (&ChatSummary{tokens: RuneCounter{}}).count(m) }
	if n, _ := pruneToolResults(msgs, 4000, count); n != 0 {
		t.Errorf("cleared %d results for a gain under an eighth of the budget", n)
	}
}

// A history that clearing brings under budget gets no summary: no side call,
// and everything said stays word for word.
func TestPruneBeforeSummarizing(t *testing.T) {
	c := testCoder(t)
	side := &summaryStub{}
	c.Summarizer = NewChatSummary(side, c.Model.SideModel, c.Tokens, c.Out, c.Clock, nil)
	c.Model.Context = 8000 // budget 4000
	for _, id := range []string{"a", "b", "c", "d"} {
		c.doneMessages = append(c.doneMessages, toolTurn(id, 1000)...)
	}
	before := c.doneMessages // shares the backing array

	c.maybeSummarize()

	if side.calls != 0 {
		t.Errorf("the side model was called %d times though clearing was enough", side.calls)
	}
	if len(c.doneMessages) != len(before) {
		t.Fatalf("messages were dropped: %d -> %d", len(before), len(c.doneMessages))
	}
	if !strings.HasPrefix(c.doneMessages[2].Text(), clearedToolResult) {
		t.Error("the oldest result was not cleared")
	}
	if strings.HasPrefix(before[2].Text(), clearedToolResult) {
		t.Error("clearing changed a message in a slice that shared the history")
	}
}

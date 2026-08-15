package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// Settled history is what gets compacted, and only a turn that edited a file
// used to produce any. A session of questions — or any stretch of /ask — kept
// everything in curMessages, which goes on the wire in full and which
// maybeSummarize never looks at. The budget existed and nothing was ever
// measured against it.
//
// The gate is a vestige of aider, where move_back_cur_messages carried the
// synthetic "I applied and committed your changes" pair and so only made sense
// after an edit. Strument removed the pair; the edit gate stayed.
func TestReadOnlyTurnsStillSettleHistory(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.curMessages = []llm.Message{
		llm.TextMessage("user", "where is Tick defined?"),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "grep", Arguments: `{"pattern":"func Tick"}`},
		}},
		llm.ToolResult("c1", "poll/poll.go:5: func Tick() int"),
		llm.TextMessage("assistant", "poll/poll.go, line 5."),
	}

	c.endTurnHistory() // what the turn-end defer does

	if len(c.curMessages) != 0 {
		t.Errorf("cur still holds %d messages after a turn ended", len(c.curMessages))
	}
	if len(c.doneMessages) != 4 {
		t.Errorf("done holds %d messages, want the turn's 4", len(c.doneMessages))
	}
}

// countMessages sums m.Text(), and an assistant message that carries only tool
// calls has no text at all — so in a harness where every action is a tool call,
// the largest part of a request counted as zero. This is not only a /tokens
// display problem: checkTokens, the guard that warns before a request overruns
// the declared window, counts the same way.
func TestTokenCountSeesToolCalls(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "edit", Arguments: `{"path":"a.go","old_string":"` +
				strings.Repeat("x", 4000) + `","new_string":"y"}`},
		}},
	}
	if got := c.countMessages(msgs); got < 900 {
		t.Errorf("a 4KB tool call counted as %d tokens; the arguments are invisible", got)
	}
}

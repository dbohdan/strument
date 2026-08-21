// What an interrupted turn leaves behind, which is the state every later
// request is built from.

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// An interrupt that caught a tool call mid-stream must not leave that call in
// the history.
//
// This is the failure the design accepted the risk of when it chose to keep the
// partial reply rather than discard it, so it gets the pinning test. An
// unanswered tool_call makes the *next* request malformed — no result will ever
// answer this one, because the interrupt landed before applyToolCalls ran — and
// the symptom is a provider error on the following send, nowhere near the
// interrupt that caused it.
func TestInterruptDropsPartialToolCalls(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "edit the file"),
		{
			Role:      llm.RoleAssistant,
			Content:   llm.TextContent("I'll start by"),
			ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "edit", Arguments: `{"path":"a.go","old`}},
		},
	}
	c.partialToolCalls = []llm.ToolCall{{ID: "call_1", Name: "edit"}}

	c.noteInterrupt()

	for i, m := range c.curMessages {
		if len(m.ToolCalls) != 0 {
			t.Fatalf("message %d (%s) still carries %d tool call(s)", i, m.Role, len(m.ToolCalls))
		}
	}
	if c.partialToolCalls != nil {
		t.Error("partialToolCalls survived the interrupt")
	}
	// The words the model did produce are its own and stay.
	if got := c.curMessages[1].Text(); got != "I'll start by" {
		t.Errorf("partial text = %q, want it preserved", got)
	}
}

// An assistant turn that was *only* an abandoned tool call leaves nothing worth
// keeping, and an empty assistant message in the history is its own problem.
func TestInterruptDropsAToolOnlyAssistantTurn(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "edit the file"),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "edit"}}},
	}

	c.noteInterrupt()

	if c.curMessages[1].Role != llm.RoleUser {
		t.Fatalf("second message is %s, want the empty assistant turn dropped",
			c.curMessages[1].Role)
	}
}

// The harness speaks in its own voice, and the roles say who spoke.
//
// Two regressions in one: the note used to be an assistant turn ("I see that
// you interrupted my previous reply"), which the model never said, and the
// marker "^C KeyboardInterrupt" used to be edited into the user's own message.
func TestInterruptFabricatesNoAssistantTurn(t *testing.T) {
	c := testCoder(t)
	typed := "rewrite the parser"
	c.curMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, typed),
		llm.TextMessage(llm.RoleAssistant, "Starting with"),
	}

	c.noteInterrupt()

	if got := c.curMessages[0].Text(); got != typed {
		t.Errorf("the user's message became %q, want %q left alone", got, typed)
	}
	last := c.curMessages[len(c.curMessages)-1]
	if last.Role != llm.RoleUser {
		t.Errorf("the note is a %s message, want %s", last.Role, llm.RoleUser)
	}
	if !strings.HasPrefix(last.Text(), llm.HarnessMarker) {
		t.Errorf("the note is not marked as the harness speaking: %q", last.Text())
	}
	for _, m := range c.curMessages {
		if m.Role == llm.RoleAssistant && m.Text() != "Starting with" {
			t.Errorf("fabricated assistant message: %q", m.Text())
		}
	}
}

// No system message may follow an assistant turn.
//
// Anthropic rejects that placement outright — "role 'system' must follow a
// 'user' message or an 'assistant' message ending in a server tool result" —
// and a live probe across five providers confirmed it is the one shape not
// universally accepted. The system role belongs to the prefix; anything the
// harness says once the conversation is under way is a marked user turn.
//
// Checked over the whole assembled request rather than over curMessages, since
// the prefix is where the legal system messages live and this is a claim about
// what goes on the wire.
func TestNoSystemMessageFollowsAnAssistantTurn(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "do the thing"),
		llm.TextMessage(llm.RoleAssistant, "Working on"),
	}
	c.noteInterrupt()
	c.curMessages = append(c.curMessages, llm.TextMessage(llm.RoleUser, "actually, do it differently"))

	msgs := c.formatChatChunks().allMessages()
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleSystem && msgs[i-1].Role == llm.RoleAssistant {
			t.Errorf("message %d is a system message following an assistant turn: %q",
				i, truncateForTest(msgs[i].Text()))
		}
	}
}

func truncateForTest(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

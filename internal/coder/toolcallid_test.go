package coder

import (
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// A provider that streams a tool call without an id must not leave the result
// unpaired: the next request would carry an empty tool_call_id, which the
// provider then refuses.
func TestToolCallWithoutIDGetsOne(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "call_abc", Name: "read"},
		{Name: "read"},
		{Name: "bash"},
	}
	fillToolCallIDs(calls, 18)
	if calls[0].ID != "call_abc" {
		t.Errorf("a provider's id was replaced: %q", calls[0].ID)
	}
	if calls[1].ID == "" || calls[2].ID == "" {
		t.Fatalf("a call is still without an id: %+v", calls)
	}
	if calls[1].ID == calls[2].ID {
		t.Errorf("two calls in one message share an id: %q", calls[1].ID)
	}
	other := []llm.ToolCall{{Name: "read"}}
	fillToolCallIDs(other, 20)
	if other[0].ID == calls[1].ID {
		t.Errorf("calls in different messages share an id: %q", other[0].ID)
	}
}

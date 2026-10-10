package coder

import (
	"context"
	"iter"
	"strings"
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
	fillToolCalls(calls, 18)
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
	fillToolCalls(other, 20)
	if other[0].ID == calls[1].ID {
		t.Errorf("calls in different messages share an id: %q", other[0].ID)
	}
}

// A call with no name keeps its place and is answered as not run, so the
// request stays well-formed and the model can call again.
func TestToolCallWithoutNameIsAnsweredNotRun(t *testing.T) {
	calls := []llm.ToolCall{{ID: "c1", Name: "read"}, {ID: "c2", Arguments: `{"path":"x.go"}`}}
	fillToolCalls(calls, 4)
	if calls[0].Name != "read" {
		t.Errorf("a named call was renamed: %q", calls[0].Name)
	}
	if calls[1].Name != unnamedToolCall {
		t.Errorf("a nameless call kept an empty name: %q", calls[1].Name)
	}
}

// bareCallThenAnswer streams one tool call with neither id nor name, as the
// Xiaomi endpoint did, then answers; it keeps the second request.
type bareCallThenAnswer struct {
	sends  int
	second llm.Request
}

func (s *bareCallThenAnswer) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.sends++
	if s.sends == 2 {
		s.second = req
	}
	first := s.sends == 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if first {
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, Args: `{"path":"."}`,
			}}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// End to end: the request after a bare call carries it with an id and a name,
// and a result paired to it that says it was not run.
func TestBareToolCallKeepsTheRequestWellFormed(t *testing.T) {
	c := testCoder(t)
	stub := &bareCallThenAnswer{}
	c.Client = stub
	c.runOne(context.Background(), "list the files")

	if stub.sends < 2 {
		t.Fatalf("the turn ended after the bare call (%d requests)", stub.sends)
	}
	var call llm.ToolCall
	var result string
	for _, m := range stub.second.Messages {
		for _, tc := range m.ToolCalls {
			call = tc
		}
		if m.Role == llm.RoleTool {
			result = m.Text()
			if m.ToolCallID != call.ID {
				t.Errorf("the result answers %q, the call is %q", m.ToolCallID, call.ID)
			}
		}
	}
	if call.ID == "" || call.Name == "" {
		t.Fatalf("the call went back out incomplete: %+v", call)
	}
	if !strings.Contains(result, "without a tool name") {
		t.Errorf("the result does not say why the call was not run: %q", result)
	}
}

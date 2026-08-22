// What the JSONL log must contain to be worth having.

package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

type capture struct{ recs []Record }

func (c *capture) Record(r Record) { c.recs = append(c.recs, r) }

func (c *capture) types() []string {
	var out []string
	for _, r := range c.recs {
		t := r.Type
		if r.Role != "" {
			t += ":" + r.Role
		}
		out = append(out, t)
	}
	return out
}

// toolThenAnswer streams reasoning and a tool call, then reasoning and prose —
// the ordinary two-send shape.
type toolThenAnswer struct{ sends int }

func (s *toolThenAnswer) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.sends++
	first := s.sends == 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventReasoning, Text: "thinking " + strings.Repeat("x", s.sends)}, nil) {
			return
		}
		if first {
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, ID: "call_1", Name: "ls", Args: `{"path":"."}`,
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

// The log is a timeline, and reasoning sits where it happened.
//
// Neither end of a flush is right. One flush can hold the user's turn, then the
// model's reply, then the results of that reply's tool calls — so reasoning
// emitted first lands ahead of the message that prompted it, and emitted last
// lands after tool results it never saw. Both were written before this test.
func TestRecordIsATimeline(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &toolThenAnswer{}

	c.runOne(context.Background(), "do the thing", false)

	got := strings.Join(rec.types(), " ")
	want := "message:user reasoning message:assistant message:tool reasoning message:assistant turn"
	if got != want {
		t.Errorf("record order:\n got %s\nwant %s", got, want)
	}
}

// A tool call's arguments and its result are both in the log.
//
// This is the half the rendered stream cannot give a scorer. The terminal shows
// "Listed . — 3 entries"; the arguments the model actually sent and the text it
// actually got back appear nowhere. One of the eleven scorer bugs counted
// "FINISHED" as a command's output when it was in the command *string*, and
// another counted "Committed " in a transcript that never carries it.
func TestRecordCarriesToolArgumentsAndResults(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &toolThenAnswer{}

	c.runOne(context.Background(), "do the thing", false)

	var call *RecordToolCall
	var result *Record
	for i, r := range rec.recs {
		if len(r.ToolCalls) > 0 {
			call = &rec.recs[i].ToolCalls[0]
		}
		if r.Role == llm.RoleTool {
			result = &rec.recs[i]
		}
	}
	if call == nil {
		t.Fatal("no tool call recorded")
	}
	if call.Name != "ls" || !strings.Contains(call.Arguments, `"path"`) {
		t.Errorf("arguments not recorded verbatim: %+v", call)
	}
	if result == nil {
		t.Fatal("no tool result recorded")
	}
	if result.ToolCallID != call.ID {
		t.Errorf("result %q does not pair with call %q", result.ToolCallID, call.ID)
	}
	if result.Text == "" {
		t.Error("the tool result is empty; the result is the point")
	}
}

// Reasoning is never mistaken for the answer, because it is never in it.
//
// The scorer bug that cost a day scored a run 0/3 by deleting its answer, and
// an earlier draft would have scored the opposite way — crediting a model that
// worked the answer out in its reasoning and then failed to say it. Separate
// records make both impossible without a delimiter to get wrong.
func TestRecordKeepsReasoningOutOfTheAnswer(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &toolThenAnswer{}

	c.runOne(context.Background(), "do the thing", false)

	for _, r := range rec.recs {
		if r.Type == "message" && strings.Contains(r.Text, "thinking") {
			t.Errorf("reasoning leaked into a %s message: %q", r.Role, r.Text)
		}
		if r.Type == "reasoning" && strings.Contains(r.Text, "Done.") {
			t.Errorf("the answer leaked into a reasoning record: %q", r.Text)
		}
	}
}

// No Recorder, no cost and no crash: the default path must be untouched.
func TestRecordIsOffByDefault(t *testing.T) {
	c := testCoder(t)
	c.Client = &toolThenAnswer{}
	if c.Recorder != nil {
		t.Fatal("a Coder starts with no Recorder")
	}
	c.runOne(context.Background(), "do the thing", false) // must not panic
}

// What the JSONL log must contain to be worth having.

package coder

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"slices"
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
// A request record follows the reply it produced, for the same reason.
func TestRecordIsATimeline(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &toolThenAnswer{}

	c.runOne(context.Background(), "do the thing")

	got := strings.Join(rec.types(), " ")
	want := "message:user reasoning message:assistant request message:tool reasoning message:assistant request turn"
	if got != want {
		t.Errorf("record order:\n got %s\nwant %s", got, want)
	}
}

func TestRecordTurnListsPinnedFiles(t *testing.T) {
	c := testCoder(t)
	path := filepath.Join(c.Root, "pinned.txt")
	if err := os.WriteFile(path, []byte("pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("pinned.txt")
	rec := &capture{}
	c.Recorder = rec
	c.Client = &toolThenAnswer{}

	c.runOne(context.Background(), "do the thing")

	var turn *Record
	for i := range rec.recs {
		if rec.recs[i].Type == "turn" {
			turn = &rec.recs[i]
		}
	}
	if turn == nil {
		t.Fatal("no turn record")
	}
	if len(turn.Pinned) == 0 {
		t.Fatal("turn record has no pinned files")
	}
	if !slices.Equal(turn.Pinned, []string{"pinned.txt"}) {
		t.Errorf("pinned = %v, want [pinned.txt]", turn.Pinned)
	}
}

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

	c.runOne(context.Background(), "do the thing")

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

	c.runOne(context.Background(), "do the thing")

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
	c.runOne(context.Background(), "do the thing") // must not panic
}

// A turn record says which mode the turn ran in and what tools the model had.
// Without it, a log where a model reported bash missing could not say whether
// it was: the header records the mode at startup only.
func TestTurnRecordNamesTheModeAndTheOfferedTools(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		wantBash bool
	}{{"tool", true}, {"ask", false}} {
		t.Run(tc.mode, func(t *testing.T) {
			c := testCoder(t)
			rec := &capture{}
			c.Recorder = rec
			c.Client = &toolThenAnswer{}
			c.SetEditFormat(tc.mode)
			c.runOne(context.Background(), "look at it")

			var turn *Record
			for i := range rec.recs {
				if rec.recs[i].Type == "turn" {
					turn = &rec.recs[i]
				}
			}
			if turn == nil {
				t.Fatal("no turn record")
			}
			if turn.EditFormat != tc.mode {
				t.Errorf("edit_format = %q, want %q", turn.EditFormat, tc.mode)
			}
			if got := slices.Contains(turn.OfferedTools, toolBash); got != tc.wantBash {
				t.Errorf("offered_tools = %v; bash offered = %v, want %v", turn.OfferedTools, got, tc.wantBash)
			}
			if !slices.Contains(turn.OfferedTools, toolRead) {
				t.Errorf("offered_tools = %v, want read in every mode", turn.OfferedTools)
			}
		})
	}
}

// usageThenAnswer fails its first send with a retryable error, then makes a
// tool call and answers, reporting usage — with a provider and a reasoning
// split — on both of the sends that succeed.
type usageThenAnswer struct{ sends int }

func (s *usageThenAnswer) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.sends++
	n := s.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		cost := 0.01 * float64(n)
		usage := &llm.Usage{PromptTokens: 1000 * n, CompletionTokens: 100, CacheReadTokens: 800,
			ReasoningTokens: 60, Provider: "Fireworks", Cost: &cost}
		switch n {
		case 1:
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrRateLimit, Message: "429"})
		case 2:
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, ID: "call_1", Name: "ls", Args: `{"path":"."}`,
			}}, nil) {
				return
			}
			if !yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: usage}, nil)
		default:
			if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
				return
			}
			if !yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: usage}, nil)
		}
	}
}

// Every request gets a record, the failed one included, and each carries its
// own usage rather than the running sum. The turn record cannot say which
// request a cost came from, or which provider a router sent it to.
func TestRecordRequests(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &usageThenAnswer{}
	c.Clock = &fastClock{}

	c.runOne(context.Background(), "do the thing")

	var reqs []Record
	for _, r := range rec.recs {
		if r.Type == "request" {
			reqs = append(reqs, r)
		}
	}
	if len(reqs) != 3 {
		t.Fatalf("%d request records, want 3 (a failure, a tool call, an answer): %v", len(reqs), rec.types())
	}

	failed := reqs[0]
	if failed.Outcome != "failed" || !strings.Contains(failed.Error, "429") {
		t.Errorf("first request: outcome %q, error %q; want failed with the 429", failed.Outcome, failed.Error)
	}
	if failed.Sent != 0 || failed.CostKnown || failed.Provider != "" {
		t.Errorf("a request with no usage reported counts: %+v", failed)
	}

	for i, r := range reqs[1:] {
		n := i + 2
		if r.Call != "turn" || r.Outcome != "done" {
			t.Errorf("request %d: call %q, outcome %q", n, r.Call, r.Outcome)
		}
		if r.Sent != 1000*n || r.Received != 100 || r.CacheRead != 800 || r.Reasoning != 60 {
			t.Errorf("request %d carries %d/%d/%d/%d, want its own usage %d/100/800/60",
				n, r.Sent, r.Received, r.CacheRead, r.Reasoning, 1000*n)
		}
		if r.Provider != "Fireworks" || !r.CostKnown {
			t.Errorf("request %d: provider %q, cost known %v", n, r.Provider, r.CostKnown)
		}
	}
	if reqs[1].FinishReason != "tool_calls" || reqs[2].FinishReason != "stop" {
		t.Errorf("finish reasons %q, %q", reqs[1].FinishReason, reqs[2].FinishReason)
	}
	// The retry repeats its step; the answer follows one completed step.
	if reqs[0].Step != 0 || reqs[1].Step != 0 || reqs[2].Step != 1 {
		t.Errorf("steps %d, %d, %d; want 0, 0, 1", reqs[0].Step, reqs[1].Step, reqs[2].Step)
	}
}

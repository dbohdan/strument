package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// The live case: Ling-3.0-tiny on llama.cpp b10902, whose Bailing V3 template
// ends the generation prompt with an open "<think>". The wire capture showed
// 164 characters of reasoning_content, no content and no tool calls, and the
// harness called that "empty".
func TestReasoningLeakNamesTheMarker(t *testing.T) {
	const trapped = "<tool_call>bash\n<arg_key>command</arg_key>\n" +
		"<arg_value>which node</arg_value>\n</tool_call>"

	marker, leaked := reasoningLeak("", trapped, 0)
	if !leaked {
		t.Fatal("a reply whose only output is a tool call in reasoning is not an empty response")
	}
	if marker != "<tool_call>" {
		t.Errorf("marker = %q, want the opener that matched", marker)
	}
}

// The counter-arms. Each is a state that must NOT be reported as a leak, or
// the check is measuring "reasoning is non-empty" rather than the phenomenon.
func TestReasoningLeakStaysQuiet(t *testing.T) {
	const trapped = "<tool_call>bash\n<arg_key>command</arg_key>\n</tool_call>"

	for _, tc := range []struct {
		name              string
		answer, reasoning string
		toolCalls         int
	}{
		{"the tool call actually arrived", "", trapped, 1},
		{"the model also answered", "Here you go.", trapped, 0},
		{"ordinary thinking, no action in it", "", "Let me look at the file first.", 0},
		{"genuinely empty", "", "", 0},
		{"reasoning is only whitespace", "", "  \n\t ", 0},
		// A closer mid-reasoning is the model talking about the syntax; only
		// one that ends the reasoning is a call cut short.
		{"a closer discussed, not ending it", "", "It must end with </tool_call>, so let me check the file.", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, leaked := reasoningLeak(tc.answer, tc.reasoning, tc.toolCalls); leaked {
				t.Error("reported a leak where there is none")
			}
		})
	}
}

// The ladder's case: the server took the openers as special tokens, and the
// reasoning ends on closing tags alone. Granite 4.2 8B on DeepInfra, verbatim.
func TestReasoningLeakClosersOnly(t *testing.T) {
	const tail = "Actually, let me just run diff via bash:\n\nI'll do that now.\n" +
		"</parameter>\n</function>\n</tool_call>\n"
	marker, leaked := reasoningLeak("", tail, 0)
	if !leaked || marker != "</tool_call>" {
		t.Errorf("leaked = %v, marker = %q; want the closer that ended the reasoning", leaked, marker)
	}
}

// leakOnceClient leaks its first reply's tool call into reasoning, as Granite
// did, and answers properly once told.
type leakOnceClient struct {
	sends    int
	requests [][]llm.Message
}

func (c *leakOnceClient) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	c.requests = append(c.requests, req.Messages)
	n := c.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		if n == 1 {
			if yield(llm.StreamEvent{Kind: llm.EventReasoning,
				Text: "Let me run the tests.\n</parameter>\n</function>\n</tool_call>"}, nil) {
				yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			}
			return
		}
		if yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
		}
	}
}

// The turn goes on after a leak: the model is told nothing ran, the user's
// message stays, and the next send carries the note after it.
func TestReasoningLeakContinuesTheTurn(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	client := &leakOnceClient{}
	c.Client = client
	out := &captureOut{}
	c.Out = out

	c.runOne(context.Background(), "run the tests")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2: the turn should go on after the leak", client.sends)
	}
	if !strings.Contains(out.String(), "Nothing ran: the model's tool call (</tool_call>)") {
		t.Errorf("the user was not told:\n%s", out.String())
	}
	msgs := client.requests[1]
	var user, note int
	for i, m := range msgs {
		switch {
		case m.Role == llm.RoleUser && strings.HasPrefix(m.Text(), llm.HarnessMarker):
			note = i
		case m.Role == llm.RoleUser && strings.Contains(m.Text(), "run the tests"):
			user = i
		}
	}
	if user == 0 || note <= user {
		t.Fatalf("want the user's message, then the note; got user at %d, note at %d", user, note)
	}
	if !strings.Contains(msgs[note].Text(), "Finish your reasoning first, then make the call.") {
		t.Errorf("note = %q", msgs[note].Text())
	}
}

// A model that leaks every time is stopped by the error-round budget, not
// sent the note forever.
func TestReasoningLeakIsBounded(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	client := &loopingLeakClient{}
	c.Client = client
	c.Out = &captureOut{}

	c.runOne(context.Background(), "run the tests")

	if want := c.MaxErrorReflections + 1; client.sends != want {
		t.Errorf("sends = %d, want %d", client.sends, want)
	}
}

type loopingLeakClient struct{ sends int }

func (c *loopingLeakClient) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	return func(yield func(llm.StreamEvent, error) bool) {
		if yield(llm.StreamEvent{Kind: llm.EventReasoning, Text: "<tool_call>bash\n</tool_call>"}, nil) {
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
		}
	}
}

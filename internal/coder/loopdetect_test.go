// Degenerate repetition: what counts as a loop, and what the turn does about it.

package coder

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// loopingClient repeats one sentence forever on the first send and answers
// normally afterwards. Forever is the point: nothing but the detector ends it,
// so a test that hangs here is a detector that does not work.
type loopingClient struct {
	sends    int
	requests [][]llm.Message
	kind     llm.EventKind
	// repeats bounds the first reply, for the one test that has to survive a
	// detector that does nothing. Zero is unbounded.
	repeats int
}

func (c *loopingClient) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	c.requests = append(c.requests, req.Messages)
	n := c.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		if n == 1 {
			for i := 0; c.repeats == 0 || i < c.repeats; i++ {
				ev := llm.StreamEvent{Kind: c.kind, Text: "I need to check the file again to be sure. "}
				if !yield(ev, nil) {
					return
				}
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "All done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func loopCoder(t *testing.T, kind llm.EventKind, answer string) (*Coder, *loopingClient, *stubAsker) {
	t.Helper()
	c := testCoder(t)
	client := &loopingClient{kind: kind}
	asker := &stubAsker{answer: answer}
	c.Client = client
	c.Asker = asker
	return c, client, asker
}

// End to end: a looping reply stops, and the turn asks what to do about it.
func TestLoopingReplyStopsTheSend(t *testing.T) {
	c, client, asker := loopCoder(t, llm.EventReasoning, "1") // "Stop"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 1 {
		t.Errorf("sends = %d, want 1 — Stop should not resume", client.sends)
	}
	if c.lastSendOutcome != OutcomeLooping {
		t.Errorf("last outcome = %v, want OutcomeLooping", c.lastSendOutcome)
	}
	if len(asker.asked) != 1 {
		t.Fatalf("asked %d questions, want 1", len(asker.asked))
	}
	// Not "you stopped the model": the user did not.
	if strings.Contains(strings.ToLower(asker.asked[0]), "you stopped") {
		t.Errorf("the question credits the user with the stop: %q", asker.asked[0])
	}
}

// The note the model reads next is the harness speaking, and it says what
// repeated. Told only "you were repeating yourself", a model has to guess which
// part, and the guess is usually the whole reply.
func TestLoopNoteIsMarkedAndQuotesTheRepeat(t *testing.T) {
	c, client, _ := loopCoder(t, llm.EventReasoning, "2") // "Try again"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2 — Try again should re-send", client.sends)
	}
	var note string
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			note = m.Text()
		}
	}
	if note == "" {
		t.Fatal("the resumed send carries no loop note")
	}
	if !strings.Contains(note, "check the file again") {
		t.Errorf("the note does not quote what repeated: %q", note)
	}
	if !strings.Contains(note, "reasoning") {
		t.Errorf("the note does not say which stream repeated: %q", note)
	}
	// The resumed send must not carry an empty user turn in place of a message.
	for i, m := range client.requests[1] {
		if m.Role == llm.RoleUser && m.Text() == "" {
			t.Errorf("message %d is an empty user turn", i)
		}
	}
}

// A loop in the answer text is caught too, and the partial the user saw stays
// in the history: it is real output, and dropping it would leave the note
// referring to nothing.
func TestLoopingAnswerKeepsThePartial(t *testing.T) {
	c, client, _ := loopCoder(t, llm.EventAnswer, "2") // "Try again"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2", client.sends)
	}
	var partial string
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleAssistant {
			partial = m.Text()
		}
	}
	if !strings.Contains(partial, "check the file again") {
		t.Errorf("the partial reply was dropped: %q", partial)
	}
}

// Turning it off means the loop is not stopped. The counter-test to all of the
// above: a setting that does nothing would pass every one of them.
//
// The client is bounded here, because with the detector off nothing else would
// ever end the reply — which is the whole point of the setting being a choice.
func TestLoopDetectionOffLetsTheLoopRun(t *testing.T) {
	c, client, asker := loopCoder(t, llm.EventReasoning, "1")
	client.repeats = 200
	c.LoopDetection = false

	c.runOne(context.Background(), "do the thing")

	if c.lastSendOutcome == OutcomeLooping {
		t.Error("a disabled detector still stopped the reply")
	}
	if len(asker.asked) != 0 {
		t.Errorf("asked %d questions with detection off, want 0", len(asker.asked))
	}
	// And the same reply, with detection on, is stopped: the pair is what makes
	// either half mean anything.
	on, onClient, _ := loopCoder(t, llm.EventReasoning, "1")
	onClient.repeats = 200
	on.runOne(context.Background(), "do the thing")
	if on.lastSendOutcome != OutcomeLooping {
		t.Errorf("last outcome = %v with detection on, want OutcomeLooping", on.lastSendOutcome)
	}
}

// A steered turn's answer includes the interrupted content so the transcript
// records the full arc: what the model was saying, the steer, and the final
// answer. Without this the transcript shows only the last send's output, and
// the interrupted attempt vanishes.
func TestSteeredTurnIncludesInterruptedContent(t *testing.T) {
	// "Try again" — the model loops, is stopped, told to try again, succeeds.
	c, _, _ := loopCoder(t, llm.EventAnswer, "2")
	got := c.Run(context.Background(), "do the thing")

	if !strings.Contains(got, "I need to check the file again") {
		t.Errorf("answer does not include the interrupted content:\n%s", got)
	}
	if !strings.Contains(got, "All done.") {
		t.Errorf("answer does not include the final content:\n%s", got)
	}
}

// A custom steer appears in the answer so the transcript shows what the user
// told the model to do between the interrupted send and the final one.
func TestSteeredTurnIncludesSteerMessage(t *testing.T) {
	c, _, _ := loopCoder(t, llm.EventAnswer, "You got stuck. Focus on contain.go first.")
	got := c.Run(context.Background(), "do the thing")

	if !strings.Contains(got, "> You got stuck. Focus on contain.go first.") {
		t.Errorf("answer does not include the steer as a blockquote:\n%s", got)
	}
}

// When the user stops after a loop, the interrupted content is still the
// answer — nothing is accumulated because there is no continuation.
func TestStoppedTurnReturnsInterruptedContent(t *testing.T) {
	c, client, _ := loopCoder(t, llm.EventAnswer, "1") // "Stop"
	c.runOne(context.Background(), "do the thing")

	if client.sends != 1 {
		t.Errorf("sends = %d, want 1 — Stop should not resume", client.sends)
	}
	if c.turnHistory != "" {
		t.Errorf("turnHistory = %q after Stop, want empty", c.turnHistory)
	}
}

// argLoopClient streams one write call whose content repeats three lines
// forever, then answers normally. The shape is Qwen3-VL 8B's on the ladder.
type argLoopClient struct {
	sends    int
	requests [][]llm.Message
}

func (c *argLoopClient) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	c.requests = append(c.requests, req.Messages)
	n := c.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		if n > 1 {
			if yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "All done."}, nil) {
				yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			}
			return
		}
		first := &llm.ToolCallDelta{Index: 0, ID: "w1", Name: "write", Args: `{"path": "loop.py", "content": "`}
		if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: first}, nil) {
			return
		}
		for range 5000 {
			d := &llm.ToolCallDelta{Index: 0, Args: `        self.x = 0\n        self.piece = self.types[t]\n`}
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: d}, nil) {
				return
			}
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
	}
}

// A write whose content is looping is stopped mid-stream, never runs, and
// the model is told which call it was and what repeated.
func TestLoopingToolArgumentsStopTheSend(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	client := &argLoopClient{}
	c.Client = client
	c.Asker = &stubAsker{answer: "2"} // "Try again"
	out := &captureOut{}
	c.Out = out

	c.runOne(context.Background(), "write loop.py")

	if _, err := os.Stat(filepath.Join(dir, "loop.py")); err == nil {
		t.Error("the looping write ran")
	}
	if !strings.Contains(out.String(), "Stopped the model's write call because its arguments were repeating") {
		t.Errorf("no warning naming the call:\n%s", out.String())
	}
	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2", client.sends)
	}
	var note string
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			note = m.Text()
		}
		for _, tc := range m.ToolCalls {
			t.Errorf("the stopped call was left in the history: %s", tc.Name)
		}
	}
	if !strings.Contains(note, "stopped your write call because its arguments") ||
		!strings.Contains(note, "self.x = 0") {
		t.Errorf("note = %q, want the call named and the repeat quoted", note)
	}
}

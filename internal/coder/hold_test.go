package coder

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// holdClient answers its first request with a write of made.txt, or with
// plain text when answerOnly, and its second with an answer. midStream runs
// while the first reply is still streaming, where a Ctrl-C lands. It keeps
// every request.
type holdClient struct {
	answerOnly  bool
	secondWrite bool // the second reply writes then.txt before the answer
	midStream   func()

	mu       sync.Mutex
	requests []llm.Request
}

func (s *holdClient) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	n := len(s.requests)
	s.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		if n == 1 {
			if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Writing it."}, nil) {
				return
			}
			if s.midStream != nil {
				s.midStream()
			}
			if !s.answerOnly {
				if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
					Index: 0, ID: "call_1", Name: toolWrite, Args: `{"path":"made.txt","content":"x\n"}`,
				}}, nil) {
					return
				}
				yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			return
		}
		if n == 2 && s.secondWrite {
			if yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, ID: "call_2", Name: toolWrite, Args: `{"path":"then.txt","content":"y\n"}`,
			}}, nil) {
				yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
			}
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// holdCoder is a coder whose first reply is held mid-stream, as a Ctrl-C
// holds it, and released by decide once the test has seen that nothing ran.
func holdCoder(t *testing.T, answerOnly bool, decide func(c *Coder)) (*Coder, *holdClient) {
	t.Helper()
	c := testCoder(t)
	cl := &holdClient{answerOnly: answerOnly}
	cl.midStream = func() {
		c.Hold()
		go func() {
			time.Sleep(150 * time.Millisecond)
			if _, err := os.Stat(filepath.Join(c.Root, "made.txt")); err == nil {
				t.Error("the write ran while the turn was held")
			}
			if n := len(cl.requests); n != 1 {
				t.Errorf("%d requests while held, want 1", n)
			}
			decide(c)
		}()
	}
	c.Client = cl
	return c, cl
}

func made(c *Coder) bool {
	_, err := os.Stat(filepath.Join(c.Root, "made.txt"))
	return err == nil
}

// Held, a reply's tool calls wait; released, they run and the turn goes on.
func TestHoldWaitsBeforeToolCalls(t *testing.T) {
	c, cl := holdCoder(t, false, func(c *Coder) { c.Release("", false) })
	c.Run(context.Background(), "make it")
	if !made(c) || len(cl.requests) != 2 {
		t.Errorf("after release: made=%v, %d requests; want the write and a second request", made(c), len(cl.requests))
	}
}

// Stopped at the boundary, the calls are answered as not run, so the
// conversation stays well-formed, and no further request is made.
func TestHoldStopAnswersTheCallsUnrun(t *testing.T) {
	c, cl := holdCoder(t, false, func(c *Coder) { c.Release("", true) })
	c.Run(context.Background(), "make it")
	if made(c) || len(cl.requests) != 1 {
		t.Errorf("after stop: made=%v, %d requests; want neither", made(c), len(cl.requests))
	}
	var answered bool
	for _, m := range c.doneMessages {
		if m.Role == llm.RoleTool && m.ToolCallID == "call_1" && m.Text() == unrunText {
			answered = true
		}
	}
	if !answered {
		t.Errorf("the stopped call has no result saying so: %+v", c.doneMessages)
	}
	if c.LastOutcome() != OutcomeInterrupted {
		t.Errorf("outcome = %v, want interrupted, so the REPL says the work was kept", c.LastOutcome())
	}
}

// A message typed while held rides the next request, after the tool results,
// as the user's words with a note saying when they arrived.
func TestHoldDeliversAQueuedMessageAtTheBoundary(t *testing.T) {
	c, cl := holdCoder(t, false, func(c *Coder) { c.Release("also add a test", false) })
	out := &captureOut{}
	c.Out = out
	c.Run(context.Background(), "make it")
	if len(cl.requests) != 2 {
		t.Fatalf("%d requests, want 2", len(cl.requests))
	}
	msgs := cl.requests[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || !strings.Contains(last.Text(), "also add a test") ||
		!strings.Contains(last.Text(), queuedNote) {
		t.Errorf("the second request does not end in the queued message: %+v", last)
	}
	if prev := msgs[len(msgs)-2]; prev.Role != llm.RoleTool {
		t.Errorf("the queued message should follow the tool results, got %s before it", prev.Role)
	}
	if !strings.Contains(strings.Join(out.lines, "\n"), "‹sent› also add a test") {
		t.Errorf("the delivery was not shown:\n%s", strings.Join(out.lines, "\n"))
	}
	if q := c.TakeQueued(); q != "" {
		t.Errorf("a delivered message is still queued: %q", q)
	}
}

// With no step boundary left, a message waits for the REPL, which sends it
// as the next turn.
func TestHoldKeepsAMessageWithNoBoundaryLeft(t *testing.T) {
	c, cl := holdCoder(t, true, func(c *Coder) { c.Release("and then this", false) })
	c.Run(context.Background(), "just answer")
	// The release comes after the reply ends; wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.hold.mu.Lock()
		n := len(c.hold.queued)
		c.hold.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q := c.TakeQueued(); q != "and then this" || len(cl.requests) != 1 {
		t.Errorf("queued %q after %d requests; want the message kept for the next turn", q, len(cl.requests))
	}
}

// holdAfterWrite holds the turn at the first output after made.txt exists,
// which is while the write's step is still being reported: the hold then
// lands at the boundary before the next request.
type holdAfterWrite struct {
	captureOut

	c    *Coder
	once sync.Once
	then func()
}

func (o *holdAfterWrite) check() {
	if made(o.c) {
		o.once.Do(func() { o.c.Hold(); go o.then() })
	}
}

func (o *holdAfterWrite) Toolf(format string, args ...any) {
	o.check()
	o.captureOut.Toolf(format, args...)
}

func (o *holdAfterWrite) ToolBlock(title, body string) {
	o.check()
	o.captureOut.ToolBlock(title, body)
}

func (o *holdAfterWrite) Printf(format string, args ...any) {
	o.check()
	o.captureOut.Printf(format, args...)
}

// Stopped between steps, the step's results stay, a note says where the turn
// stopped, and no further request is made.
func TestHoldStopBetweenSteps(t *testing.T) {
	c := testCoder(t)
	cl := &holdClient{}
	c.Client = cl
	out := &holdAfterWrite{c: c}
	out.then = func() {
		time.Sleep(100 * time.Millisecond)
		c.Release("", true)
	}
	c.Out = out
	c.Run(context.Background(), "make it")
	if !made(c) || len(cl.requests) != 1 {
		t.Fatalf("made=%v, %d requests; want the write and no second request", made(c), len(cl.requests))
	}
	last := c.doneMessages[len(c.doneMessages)-1]
	if !strings.Contains(last.Text(), stoppedNote) {
		t.Errorf("the turn does not end in the stop note: %+v", last)
	}
	if c.LastOutcome() != OutcomeInterrupted {
		t.Errorf("outcome = %v, want interrupted", c.LastOutcome())
	}
}

// A delivered message is a review boundary: the edits before it and after it
// are two steps for /undo, as they are around the answer to an interrupt.
func TestHoldDeliverySplitsTheUndo(t *testing.T) {
	for _, message := range []string{"", "and then.txt too"} {
		c, cl := holdCoder(t, false, func(c *Coder) { c.Release(message, false) })
		cl.secondWrite = true
		c.Run(context.Background(), "make it")
		want := 1
		if message != "" {
			want = 2
		}
		if got := len(c.undoStack); got != want {
			t.Errorf("message %q: %d undo steps, want %d", message, got, want)
		}
	}
}

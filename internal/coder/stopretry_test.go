// Ctrl-C before the model has replied: on a poor connection, during the
// retries. The turn should end as if it had not begun.

package coder

import (
	"context"
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// hangingClient stands for a connection that never answers: every send ends
// in the cancellation Ctrl-C causes, with nothing streamed. After the sends in
// answerFrom it answers normally, for tests that need a first send to work.
type hangingClient struct {
	sends      int
	answerFrom int // 0 means never
	firstTool  bool
}

func (c *hangingClient) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	n := c.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		if c.firstTool && n == 1 {
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, ID: "call_1", Name: "ls", Args: `{"path":"."}`,
			}}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
			return
		}
		if c.answerFrom == 0 || n < c.answerFrom {
			yield(llm.StreamEvent{}, context.Canceled)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func stopCoder(t *testing.T, client llm.ModelClient) (*Coder, *capture, *captureOut, *stubAsker) {
	t.Helper()
	c := testCoder(t)
	rec := &capture{}
	out := &captureOut{}
	asker := &stubAsker{}
	c.Recorder = rec
	c.Out = out
	c.Asker = asker
	c.Client = client
	c.Clock = &fastClock{}
	return c, rec, out, asker
}

func userTurns(c *Coder) int {
	n := 0
	for _, m := range slices.Concat(c.doneMessages, c.curMessages) {
		if m.Role == llm.RoleUser {
			n++
		}
	}
	return n
}

func hasTurnRecord(rec *capture) bool {
	for _, r := range rec.recs {
		if r.Type == "turn" {
			return true
		}
	}
	return false
}

// Stopped while the connection was being waited on: no question, nothing in
// the conversation, no turn in the record, and the attachments given back.
func TestStopBeforeReplyEndsTheTurnQuietly(t *testing.T) {
	c, rec, out, asker := stopCoder(t, &hangingClient{})
	img := llm.ImageSource{MediaType: "image/png", Data: "iVBORw0KGgo=", Label: "shot.png"}
	c.pendingAttachments = []llm.ImageSource{img}

	c.runOne(context.Background(), "do the thing")

	if c.lastSendOutcome != OutcomeStopped {
		t.Errorf("outcome = %v, want Stopped", c.lastSendOutcome)
	}
	if len(asker.asked) != 0 {
		t.Errorf("asked %q; a stop before any reply has nothing to ask about", asker.asked)
	}
	if n := userTurns(c); n != 0 {
		t.Errorf("%d user messages in the conversation, want none", n)
	}
	if hasTurnRecord(rec) {
		t.Error("a turn record was written for a turn that never got a reply")
	}
	if len(c.pendingAttachments) != 1 || c.pendingAttachments[0].Label != "shot.png" {
		t.Errorf("attachments = %v, want the image back", c.pendingAttachments)
	}
	if !slices.ContainsFunc(out.lines, func(l string) bool { return strings.Contains(l, "Stopped before the model replied") }) {
		t.Errorf("no line saying the turn was stopped: %q", out.lines)
	}
}

// retryThenHang fails the first attempt with a retryable error, so the send
// waits between attempts; the clock's Sleep is where Ctrl-C lands.
type retryThenHang struct{ sends int }

func (c *retryThenHang) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrNetwork, Message: "connection reset"})
	}
}

// interruptingClock presses Ctrl-C during the first wait.
type interruptingClock struct{ c *Coder }

func (k *interruptingClock) Sleep(ctx context.Context, _ time.Duration) bool {
	k.c.InterruptSend()
	return ctx.Err() == nil
}
func (k *interruptingClock) Now() time.Time { return time.Now() }

// Stopped during the wait between attempts: the same quiet end, and not
// "gave up: context canceled", which reads as a failure.
func TestStopDuringRetryWaitEndsTheTurnQuietly(t *testing.T) {
	client := &retryThenHang{}
	c, rec, out, asker := stopCoder(t, client)
	c.Clock = &interruptingClock{c}

	c.runOne(context.Background(), "do the thing")

	if client.sends != 1 {
		t.Errorf("sends = %d, want 1: the retry should not have gone out", client.sends)
	}
	if c.lastSendOutcome != OutcomeStopped {
		t.Errorf("outcome = %v, want Stopped", c.lastSendOutcome)
	}
	if len(asker.asked) != 0 || userTurns(c) != 0 || hasTurnRecord(rec) {
		t.Errorf("asked %q, %d user messages, turn record %v; want none of them",
			asker.asked, userTurns(c), hasTurnRecord(rec))
	}
	for _, l := range out.lines {
		if strings.Contains(l, "gave up") {
			t.Errorf("reported as a failure: %q", l)
		}
	}
}

// Once something in the turn has happened, an interrupt is a steer again,
// even if this send had produced nothing yet.
func TestStopAfterAToolCallStillAsks(t *testing.T) {
	c, _, _, asker := stopCoder(t, &hangingClient{firstTool: true})

	c.runOne(context.Background(), "list the files")

	if c.lastSendOutcome != OutcomeInterrupted {
		t.Errorf("outcome = %v, want Interrupted", c.lastSendOutcome)
	}
	if len(asker.asked) != 1 {
		t.Errorf("asked %d questions, want the usual one", len(asker.asked))
	}
}

// The next message after a stop is an ordinary first turn: the stopped one
// left nothing behind for it to carry.
func TestTheTurnAfterAStopStartsClean(t *testing.T) {
	client := &hangingClient{answerFrom: 2}
	c, _, _, asker := stopCoder(t, client)

	c.runOne(context.Background(), "first try")
	c.runOne(context.Background(), "second try")

	if c.lastSendOutcome != OutcomeSuccess {
		t.Fatalf("outcome = %v, want Success", c.lastSendOutcome)
	}
	var texts []string
	for _, m := range slices.Concat(c.doneMessages, c.curMessages) {
		if m.Role == llm.RoleUser {
			texts = append(texts, m.Text())
		}
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "second try") {
		t.Errorf("user messages = %q, want only the second", texts)
	}
	if len(asker.asked) != 0 {
		t.Errorf("asked %q", asker.asked)
	}
}

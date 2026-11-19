package repl

import (
	"context"
	"io"
	"iter"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// heldReplyClient streams a little of its first reply, then waits for finish
// or a cancel; later replies answer at once. It keeps every request.
type heldReplyClient struct {
	started chan struct{}
	finish  chan struct{}

	mu       sync.Mutex
	requests []llm.Request
}

func (h *heldReplyClient) Send(ctx context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	h.mu.Lock()
	h.requests = append(h.requests, req)
	n := len(h.requests)
	h.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		if n > 1 {
			if yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Second."}, nil) {
				yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			}
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Working on it."}, nil) {
			return
		}
		close(h.started)
		select {
		case <-ctx.Done():
			yield(llm.StreamEvent{}, ctx.Err())
			return
		case <-h.finish:
		}
		if yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: " Done."}, nil) {
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
		}
	}
}

func (h *heldReplyClient) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

// holdREPL starts a REPL on cl, sends "hello", presses Ctrl-C once the reply
// is streaming, and waits for the menu. It returns the pipe to type the
// answer into, the output, and the channel Run's result arrives on.
func holdREPL(t *testing.T, cl *heldReplyClient, setup ...func(*REPL)) (*io.PipeWriter, interface{ String() string }, chan error) {
	t.Helper()
	return holdREPLOn(t, cl, "hello", setup...)
}

// holdREPLOn is holdREPL with the first line given.
func holdREPLOn(t *testing.T, cl *heldReplyClient, first string, setup ...func(*REPL)) (*io.PipeWriter, interface{ String() string }, chan error) {
	t.Helper()
	pr, pw := io.Pipe()
	r, _, out := newTestREPL(t, cl, pr)
	for _, f := range setup {
		f(r)
	}
	t.Cleanup(func() { _ = r.Close() })
	var sig chan<- os.Signal
	ready := make(chan struct{})
	r.opts.Notify = func(ch chan<- os.Signal) func() {
		if sig == nil {
			sig = ch
			close(ready)
		}
		return func() {}
	}
	r.opts.Exit = func(code int) { t.Errorf("exited with %d", code) }
	go func() { _, _ = io.WriteString(pw, first+"\n") }()
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	<-ready
	<-cl.started
	sig <- os.Interrupt
	waitFor(t, out, "‹paused›")
	return pw, out, done
}

func waitFor(t *testing.T, out interface{ String() string }, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in:\n%s", s, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func finishRun(t *testing.T, pw *io.PipeWriter, done chan error) {
	t.Helper()
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the REPL did not stop")
	}
}

// While held, the reply keeps streaming but none of it shows; on Enter it all
// appears, in order, and the turn ends normally.
func TestHoldMenuContinue(t *testing.T) {
	cl := &heldReplyClient{started: make(chan struct{}), finish: make(chan struct{})}
	pw, out, done := holdREPL(t, cl)
	close(cl.finish)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "Done.") {
		t.Errorf("the reply showed through the menu:\n%s", out.String())
	}
	_, _ = io.WriteString(pw, "\n")
	waitFor(t, out, "Done.")
	finishRun(t, pw, done)
	if cl.count() != 1 {
		t.Errorf("%d requests, want 1", cl.count())
	}
}

// s stops the turn: the reply is cut off and the REPL says the work was kept.
func TestHoldMenuStop(t *testing.T) {
	cl := &heldReplyClient{started: make(chan struct{}), finish: make(chan struct{})}
	pw, out, done := holdREPL(t, cl)
	_, _ = io.WriteString(pw, "s\n")
	waitFor(t, out, "Stopping the turn.")
	waitFor(t, out, "Completed work and conversation context are retained")
	finishRun(t, pw, done)
	if cl.count() != 1 || strings.Contains(out.String(), "Done.") {
		t.Errorf("after stop: %d requests, output:\n%s", cl.count(), out.String())
	}
}

// A message typed into the menu, when the turn has no step left to deliver it
// at, is sent as the next turn once this one ends.
func TestHoldMenuQueuesTheNextTurn(t *testing.T) {
	cl := &heldReplyClient{started: make(chan struct{}), finish: make(chan struct{})}
	pw, out, done := holdREPL(t, cl)
	_, _ = io.WriteString(pw, "and this too\n")
	waitFor(t, out, "Queued for the model's next step.")
	close(cl.finish)
	waitFor(t, out, "Second.")
	finishRun(t, pw, done)
	if cl.count() != 2 {
		t.Fatalf("%d requests, want 2", cl.count())
	}
	msgs := cl.requests[1].Messages
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleUser || !strings.Contains(last.Text(), "and this too") {
		t.Errorf("the second turn is not the queued message: %+v", last)
	}
}

// A Ctrl-C at the menu after the chord's window has passed asks again rather
// than carrying on, so the next one exits.
func TestHoldMenuLateCtrlCAsksAgain(t *testing.T) {
	cl := &heldReplyClient{started: make(chan struct{}), finish: make(chan struct{})}
	var mu sync.Mutex
	clock := time.Unix(0, 0)
	pw, out, done := holdREPL(t, cl, func(r *REPL) {
		r.opts.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			clock = clock.Add(5 * time.Second)
			return clock
		}
	})
	_, _ = io.WriteString(pw, "\x03")
	waitFor(t, out, "Press Ctrl-C again to exit")
	close(cl.finish)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "Done.") {
		t.Fatalf("the late Ctrl-C carried the turn on:\n%s", out.String())
	}
	_, _ = io.WriteString(pw, "\n")
	waitFor(t, out, "Done.")
	finishRun(t, pw, done)
}

// A message typed at the menu during a /btw is the next turn once the /btw
// ends, not something left for a later turn to deliver mid-way.
func TestHoldMenuDuringBtwQueuesTheNextTurn(t *testing.T) {
	cl := &heldReplyClient{started: make(chan struct{}), finish: make(chan struct{})}
	pw, out, done := holdREPLOn(t, cl, "/btw what is a lighthouse?")
	_, _ = io.WriteString(pw, "and this too\n")
	waitFor(t, out, "Queued for the model's next step.")
	close(cl.finish)
	waitFor(t, out, "Second.")
	finishRun(t, pw, done)
	if cl.count() != 2 {
		t.Fatalf("%d requests, want 2", cl.count())
	}
	msgs := cl.requests[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || !strings.Contains(last.Text(), "and this too") ||
		strings.Contains(last.Text(), "while you were working") {
		t.Errorf("the turn after the /btw is not the plain queued message: %+v", last)
	}
}

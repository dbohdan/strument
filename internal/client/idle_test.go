package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"

	"dbohdan.com/strument/internal/config"
)

// stallingServer sends headers and a first chunk, then holds the connection
// open and sends nothing more. That is the failure this guards: not a dropped
// connection (which surfaces as a read error), not a slow one (which keeps
// sending), but a live socket that has stopped producing.
func stallingServer(t *testing.T, release <-chan struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		flush(t, w)
		<-release // never sends [DONE]
	}))
}

func TestStalledStreamFailsInsteadOfHanging(t *testing.T) {
	release := make(chan struct{})
	srv := stallingServer(t, release)
	// Order matters and LIFO is the trap: httptest's Close waits for handlers
	// to return, and this handler returns only when release closes. Registering
	// Close first means it runs last, after the release that unblocks it.
	defer srv.Close()
	defer close(release)

	c := &Client{
		Provider:          config.Provider{BaseURL: srv.URL, APIKey: "k"},
		StreamIdleTimeout: 150 * time.Millisecond,
	}

	done := make(chan error, 1)
	go func() {
		var last error
		for ev, err := range c.Send(context.Background(), llm.Request{Model: "m"}) {
			_ = ev
			if err != nil {
				last = err
				break
			}
		}
		done <- last
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled stream ended without an error")
		}
		var se *llm.StreamError
		if !errors.As(err, &se) {
			t.Fatalf("error is %T, want *llm.StreamError: %v", err, err)
		}
		if se.Class != llm.ErrNetwork {
			t.Errorf("class = %v, want ErrNetwork so the retry path takes it", se.Class)
		}
		if !strings.Contains(se.Message, "stopped sending") {
			t.Errorf("message = %q, want it to say the provider stopped sending", se.Message)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the send hung on a stalled stream; the idle timer did not fire")
	}
}

// A stream that keeps producing must not be cut off, however long it runs:
// the timer bounds the gap between bytes, not the total.
func TestSlowButLiveStreamIsNotCutOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Ten chunks at half the idle timeout apart: total well over it.
		for range 10 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			flush(t, w)
			time.Sleep(75 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush(t, w)
	}))
	defer srv.Close()

	c := &Client{
		Provider:          config.Provider{BaseURL: srv.URL, APIKey: "k"},
		StreamIdleTimeout: 150 * time.Millisecond,
	}
	var got int
	for ev, err := range c.Send(context.Background(), llm.Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("a live stream was cut off after %d chunks: %v", got, err)
		}
		if ev.Kind == llm.EventAnswer {
			got++
		}
	}
	if got != 10 {
		t.Errorf("got %d chunks, want 10", got)
	}
}

// flush pushes bytes to the wire. Without it the chunks sit in a buffer and
// the reader sees one long silence followed by everything at once, which is
// the opposite of what these tests are about.
func flush(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	f, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("the test server's ResponseWriter cannot flush")
	}
	f.Flush()
}

// A request killed by its own deadline has to arrive as something the retry
// path can classify and a person can read. Bare, it was neither: errors.As
// failed on it, so retryBackoff took its non-retryable branch by accident, and
// the user was shown Go's "context deadline exceeded".
func TestContextErrorIsTypedAndStillTheSentinel(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"deadline", context.DeadlineExceeded, "ran out of time"},
		{"cancel", context.Canceled, "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contextError(tc.err)

			var se *llm.StreamError
			if !errors.As(got, &se) {
				t.Fatalf("%v is not a StreamError, so the retry path cannot classify it", got)
			}
			if se.Retryable() {
				t.Error("a spent budget was marked retryable; there is nothing left to retry into")
			}
			if !strings.Contains(se.Message, tc.want) {
				t.Errorf("message = %q, want it to say %q in the user's terms", se.Message, tc.want)
			}
			if strings.Contains(se.Message, "context") {
				t.Errorf("message = %q still names Go's plumbing", se.Message)
			}
			// The classification must not hide the cause from callers.
			if !errors.Is(got, tc.err) {
				t.Errorf("%v is no longer errors.Is %v", got, tc.err)
			}
		})
	}
}

// The classifier has to be wired in, not merely defined. This drives a real
// Send against a server that never answers and lets the context expire, which
// is the shape of the failure a side call hits when its budget runs out.
func TestSendReportsAnExpiredContextReadably(t *testing.T) {
	// Bounded rather than blocking on r.Context().Done(): httptest's Close
	// waits for outstanding handlers, and a handler that only returns when the
	// client disconnects deadlocks the test rather than the product.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(2 * time.Second) // far longer than the context below
	}))
	defer srv.Close()

	c := &Client{Provider: config.Provider{BaseURL: srv.URL, APIKey: "k"}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var got error
	for _, err := range c.Send(ctx, llm.Request{Model: "m", Messages: []llm.Message{llm.TextMessage("user", "hi")}}) {
		if err != nil {
			got = err
		}
	}

	if got == nil {
		t.Fatal("an expired request reported no error")
	}
	var se *llm.StreamError
	if !errors.As(got, &se) {
		t.Fatalf("error %v is not a StreamError, so retryBackoff cannot classify it", got)
	}
	if strings.Contains(got.Error(), "context deadline exceeded") {
		t.Errorf("the user is still shown Go's phrasing: %v", got)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("%v no longer identifies as a deadline to callers that check", got)
	}
}

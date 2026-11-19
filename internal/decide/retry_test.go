package decide

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dbohdan.com/strument/internal/httpx"
)

const okBody = `{"model":"m","answers":{"decision":{"type":"choice","probabilities":{"safe":0.91,"ask":0.09}}}}`

func quickRetries(t *testing.T) {
	t.Helper()
	saved := httpx.RetryDelays
	httpx.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { httpx.RetryDelays = saved })
}

// A service that says to come back, or drops the connection, is asked again;
// the decision that follows is the answer.
func TestDecisionRetriesTransientFailures(t *testing.T) {
	quickRetries(t)
	for _, tc := range []struct {
		name  string
		first func(w http.ResponseWriter)
	}{
		{"503", func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }},
		{"429", func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) }},
		{"dropped connection", func(w http.ResponseWriter) {
			if h, ok := w.(http.Hijacker); ok {
				if conn, _, err := h.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if n.Add(1) <= 2 {
					tc.first(w)
					return
				}
				_, _ = w.Write([]byte(okBody))
			}))
			defer srv.Close()
			d, err := NewSystemOne(srv.URL, "laya", "", nil, "test")(context.Background(), Input{Command: "ls"})
			if err != nil || d.PSafe != 0.91 || n.Load() != 3 {
				t.Errorf("d=%+v err=%v after %d requests; want the decision on the third", d, err, n.Load())
			}
		})
	}
}

// An answer that will not change on asking again is returned at once.
func TestDecisionDoesNotRetryAnAnswer(t *testing.T) {
	quickRetries(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	_, err := NewSystemOne(srv.URL, "laya", "", nil, "test")(context.Background(), Input{Command: "ls"})
	if err == nil || n.Load() != 1 {
		t.Errorf("err=%v after %d requests; want an error after one", err, n.Load())
	}
}

// A stuck handshake fails in seconds, leaving time to try again, where the
// default transport would wait out the whole decision.
func TestDecisionHandshakeIsShort(t *testing.T) {
	// End to end: a listener that accepts and never speaks TLS. It waits out
	// one handshake, so it is not in the short run.
	if testing.Short() {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	saved := httpx.RetryDelays
	httpx.RetryDelays = nil
	defer func() { httpx.RetryDelays = saved }()
	start := time.Now()
	_, err = NewSystemOne("https://"+ln.Addr().String(), "laya", "", nil, "test")(context.Background(), Input{Command: "ls"})
	if took := time.Since(start); err == nil || !strings.Contains(err.Error(), "TLS handshake timeout") ||
		took > httpx.HandshakeTimeout+2*time.Second {
		t.Errorf("err=%v after %s", err, took)
	}
}

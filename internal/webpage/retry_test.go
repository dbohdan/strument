package webpage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dbohdan.com/strument/internal/httpx"
)

func quickRetries(t *testing.T) {
	t.Helper()
	saved := httpx.RetryDelays
	httpx.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { httpx.RetryDelays = saved })
}

// A fetch that meets a busy server or a dropped connection is tried again,
// and the page that follows is the answer.
func TestFetchRetriesTransientFailures(t *testing.T) {
	quickRetries(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch n.Add(1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			if h, ok := w.(http.Hijacker); ok {
				if conn, _, err := h.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("the page"))
		}
	}))
	defer srv.Close()
	out, err := NewSimple(nil, "")(context.Background(), srv.URL+"/a.txt", Options{})
	if err != nil || !strings.Contains(out, "the page") || n.Load() != 3 {
		t.Errorf("out=%q err=%v after %d requests; want the page on the third", out, err, n.Load())
	}

	// A status that will not change is reported after one request.
	n.Store(0)
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	if _, err := NewSimple(nil, "")(context.Background(), missing.URL, Options{}); err == nil || n.Load() != 1 {
		t.Errorf("404: err=%v after %d requests", err, n.Load())
	}
}

// The refusals are the caller's decisions, not failures, and are not tried
// again: a redirect off the approved origin, and a local address. The
// scraper returns them bare, so a retry is counted where it would show, in
// how often the policy is asked.
func TestFetchDoesNotRetryARefusal(t *testing.T) {
	quickRetries(t)
	from, _, hits := redirectPair(t)
	var asked atomic.Int32
	never := func(string) bool { asked.Add(1); return false }
	_, err := NewSimple(nil, "")(context.Background(), from.URL+"/go", Options{Follow: never})
	var refused *RedirectRefusedError
	if !errors.As(err, &refused) || asked.Load() != 1 || hits.Load() != 0 {
		t.Errorf("a refused redirect: err=%v, the policy asked %d times", err, asked.Load())
	}

	srv, localHits := localServer(t)
	asked.Store(0)
	_, err = NewSimple(nil, "")(context.Background(), srv.URL, Options{LocalOK: never})
	var local *LocalAddressError
	if !errors.As(err, &local) || asked.Load() > 2 || localHits.Load() != 0 {
		t.Errorf("a refused local address: err=%v, the policy asked %d times", err, asked.Load())
	}
}

package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"dbohdan.com/strument/internal/httpx"
)

// The other tests here serve 429s and 5xx answers to see them reported;
// waiting out the real delays between attempts would take most of a minute.
func TestMain(m *testing.M) {
	httpx.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	os.Exit(m.Run())
}

// A search that meets a busy service or a dropped connection is asked again,
// for every backend.
func TestSearchRetriesTransientFailures(t *testing.T) {
	for name, build := range map[string]func(url string) Searcher{
		"anysearch": func(u string) Searcher { return NewAnySearch(u, "", nil, "test") },
		"exa":       func(u string) Searcher { return NewExa(u, "k", nil, "test") },
		"searxng":   func(u string) Searcher { return NewSearxNG(u, nil, "test") },
	} {
		t.Run(name, func(t *testing.T) {
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
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"results": []}`))
				}
			}))
			defer srv.Close()
			_, err := build(srv.URL)(context.Background(), "q")
			if n.Load() != 3 {
				t.Errorf("%d requests, want 3 (err=%v)", n.Load(), err)
			}
		})
	}
}

// Redirects under webfetch: the user approves an origin, and a redirect must
// not carry the fetch to one they did not. Two loopback servers on different
// ports are two origins, which is all the test needs; nothing leaves the host.

package webpage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// redirectPair is an origin whose /go redirects to another origin's page, and
// a counter of how often that other page was served.
func redirectPair(t *testing.T) (from, to *httptest.Server, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	to = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("the other origin's page\n"))
	}))
	from = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/go":
			http.Redirect(w, r, to.URL+"/page", http.StatusFound)
		case "/moved":
			http.Redirect(w, r, "/here", http.StatusMovedPermanently)
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("same origin page\n"))
		}
	}))
	t.Cleanup(from.Close)
	t.Cleanup(to.Close)
	return from, to, hits
}

func TestScraperStopsAtARedirectFollowRefuses(t *testing.T) {
	from, to, hits := redirectPair(t)
	scrape := NewSimple(nil, "")
	never := func(string) bool { return false }

	_, err := scrape(context.Background(), from.URL+"/go", Options{Follow: never})
	var refused *RedirectRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a RedirectRefusedError", err)
	}
	if refused.To != to.URL+"/page" {
		t.Errorf("refused.To = %q, want the target so the model can fetch it itself", refused.To)
	}
	if hits.Load() != 0 {
		t.Error("the refused origin was contacted")
	}

	// Within one origin a redirect is followed whatever Follow says: the user
	// approved the origin, and /moved -> /here is still it.
	out, err := scrape(context.Background(), from.URL+"/moved", Options{Follow: never})
	if err != nil || !strings.Contains(out, "same origin page") {
		t.Errorf("a same-origin redirect was not followed: %q, %v", out, err)
	}
}

// nil Follow is /web's: the user typed the URL and reads the result, so
// redirects are followed the way a browser follows them.
func TestScraperFollowsEveryRedirectWithoutAPolicy(t *testing.T) {
	from, _, hits := redirectPair(t)
	out, err := NewSimple(nil, "")(context.Background(), from.URL+"/go", Options{})
	if err != nil || !strings.Contains(out, "the other origin's page") || hits.Load() != 1 {
		t.Errorf("out = %q, err = %v, hits = %d; want the redirect followed", out, err, hits.Load())
	}
}

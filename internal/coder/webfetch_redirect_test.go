package coder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dbohdan.com/strument/internal/webpage"
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

// The case the rule exists for, end to end through the tool. The approved
// origin redirects elsewhere; the other page must not be fetched, and the model
// must be told where it went so that fetching it is its own, asked-about call.
func TestWebfetchDoesNotFollowARedirectToAnUnapprovedOrigin(t *testing.T) {
	from, to, hits := redirectPair(t)
	c, cf, _ := fetchCoder(t, false)
	c.Scrape = webpage.NewSimple(nil, "")
	c.WebfetchAllow = []string{strings.TrimPrefix(from.URL, "http://")}

	out := runFetch(t, c, fetchCall(from.URL+"/go", "read it"))
	if hits.Load() != 0 {
		t.Fatalf("an approved origin's redirect fetched an unapproved one: %q", out)
	}
	if !strings.Contains(out, to.URL+"/page") || !strings.Contains(out, "not followed") {
		t.Errorf("result = %q, want the redirect target named and the refusal stated", out)
	}
	if len(cf.got) != 0 {
		t.Errorf("asked %d times; the redirect is refused, not prompted about mid-fetch", len(cf.got))
	}

	// Once the target's origin is approved too, the same redirect goes through.
	c.WebfetchAllow = append(c.WebfetchAllow, strings.TrimPrefix(to.URL, "http://"))
	if out := runFetch(t, c, fetchCall(from.URL+"/go", "read it")); !strings.Contains(out, "the other origin's page") {
		t.Errorf("a redirect between two approved origins was not followed: %q", out)
	}
}

package coder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// anySearchServer answers like the service, recording what it was sent so a
// test can assert on the request as well as the reply.
func anySearchServer(t *testing.T, status int, body string) (*httptest.Server, *http.Header, *map[string]any) {
	t.Helper()
	var gotHeader http.Header
	gotBody := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("path = %q, want /v1/search", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		gotHeader = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotHeader, &gotBody
}

// The envelope a live call actually returned, trimmed to the fields used. Both
// snippet and content come back and are usually identical.
const anySearchLiveBody = `{
  "code": 0, "message": "success", "request_id": "bf58c258-ae80-4cdc-b8e4-8f1a77ac001e",
  "data": {
    "results": [
      {"title": "GitHub - chzyer/readline", "url": "https://github.com/chzyer/readline",
       "snippet": "The most popular multi-platform readline library for Go.",
       "content": "The most popular multi-platform readline library for Go, with line editing."},
      {"title": "readline package", "url": "https://pkg.go.dev/github.com/chzyer/readline",
       "snippet": "Readline is a pure go implementation.", "content": ""},
      {"title": "no url here", "url": "", "snippet": "dropped"}
    ],
    "metadata": {"total_results": 3, "search_time_ms": 883}
  }
}`

func TestAnySearchDecodesALiveShape(t *testing.T) {
	srv, hdr, body := anySearchServer(t, 200, anySearchLiveBody)
	res, err := NewAnySearch(srv.URL, "as_sk_test", nil, "Strument/test")(
		context.Background(), "go readline library")
	if err != nil {
		t.Fatal(err)
	}
	if got := (*body)["query"]; got != "go readline library" {
		t.Errorf("query sent = %v", got)
	}
	if got := (*body)["max_results"]; got != float64(searchMaxResults) {
		t.Errorf("max_results sent = %v, want the service's cap of %d", got, searchMaxResults)
	}
	if got := hdr.Get("Authorization"); got != "Bearer as_sk_test" {
		t.Errorf("Authorization = %q", got)
	}

	// A result with no URL is dropped wherever it sits: it is a line a reader
	// cannot act on.
	if len(res.Results) != 2 {
		t.Fatalf("got %d results, want 2 (the third has no URL)", len(res.Results))
	}
	// content wins where both are present, because it is the fuller of the two.
	if !strings.Contains(res.Results[0].Content, "with line editing") {
		t.Errorf("content did not win over snippet: %q", res.Results[0].Content)
	}
	// And snippet is the fallback where content is empty, or the result would
	// arrive as a bare title and URL.
	if res.Results[1].Content != "Readline is a pure go implementation." {
		t.Errorf("snippet was not used as the fallback: %q", res.Results[1].Content)
	}
	// AnySearch reports no per-engine health, so nothing is invented here.
	if len(res.Unresponsive) != 0 {
		t.Errorf("Unresponsive = %v, want none — the service reports no engine health", res.Unresponsive)
	}
}

// The key is optional, and keeping it so matters: a search tool that demands a
// signup before it does anything is one most people never turn on. Confirmed
// against the live service, which answers anonymously.
func TestAnySearchWorksWithoutAKey(t *testing.T) {
	srv, hdr, _ := anySearchServer(t, 200, anySearchLiveBody)
	if _, err := NewAnySearch(srv.URL, "", nil, "Strument/test")(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if got := hdr.Get("Authorization"); got != "" {
		t.Errorf("an empty key still sent Authorization: %q", got)
	}
	// And Strument identifies as itself rather than as the vendor's own client.
	if got := hdr.Get("User-Agent"); !strings.HasPrefix(got, "Strument/") {
		t.Errorf("User-Agent = %q", got)
	}
	if got := hdr.Get("X-Anysearch-Client"); got != "" {
		t.Errorf("claimed to be the vendor's CLI: %q", got)
	}
}

func TestAnySearchTranslatesFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{
			// The live shape for a bad key: the status and the code agree here.
			name: "rejected key", status: 401,
			body: `{"code":-1,"message":"Invalid API key.","request_id":"1d73440a"}`,
			want: "answers anonymously",
		},
		{
			// A failure can also arrive as code != 0 with an HTTP 200 — the
			// vendor's own client treats the code as authoritative, so a
			// status-only check would read this as a successful empty search.
			name: "failure under a 200", status: 200,
			body: `{"code":-1,"message":"Quota exceeded.","request_id":"abc123"}`,
			want: "Quota exceeded",
		},
		{
			name: "rate limited", status: 429,
			body: `{"code":-1,"message":"Rate limited."}`,
			want: "rate-limiting",
		},
		{
			// Something that is not the service answering — a proxy, a captive
			// portal — must not be reported as the service changing its format.
			name: "not JSON at all", status: 502,
			body: `<html>Bad Gateway</html>`,
			want: "HTTP 502",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := anySearchServer(t, tc.status, tc.body)
			_, err := NewAnySearch(srv.URL, "k", nil, "")(context.Background(), "q")
			if err == nil {
				t.Fatal("no error at all")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error was %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A 200 carrying code -1 is the trap this backend has, in the same family as
// SearXNG's 200 carrying HTML: it looks like a successful search that found
// nothing, which is a different thing to tell a user.
func TestAnySearchDoesNotReadAFailureAsEmpty(t *testing.T) {
	srv, _, _ := anySearchServer(t, 200, `{"code":-1,"message":"Quota exceeded.","data":{"results":[]}}`)
	res, err := NewAnySearch(srv.URL, "k", nil, "")(context.Background(), "q")
	if err == nil {
		t.Fatalf("a failed search was reported as %d results", len(res.Results))
	}
}

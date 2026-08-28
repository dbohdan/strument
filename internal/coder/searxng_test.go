package coder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// searxServer answers like an instance, so a test exercises the real HTTP path
// rather than a hand-built struct.
func searxServer(t *testing.T, status int, ctype, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("the request was not form-encoded: %v", err)
		}
		if got := r.Form.Get("format"); got != "json" {
			t.Errorf("format = %q, want json — the instance would return HTML", got)
		}
		if r.Form.Get("q") == "" {
			t.Error("no query was sent")
		}
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The shape a live instance actually returns, trimmed to the fields used. The
// three unresponsive engines are verbatim from a real run: this is what a
// *successful* query looks like, not a bad day.
const searxLiveBody = `{
  "query": "golang http client timeout",
  "results": [
    {"url": "https://pkg.go.dev/net/http", "title": "net/http", "content": "Package http provides.",
     "engine": "google", "score": 3.5, "positions": [1], "parsed_url": ["https", "pkg.go.dev"],
     "template": "default.html", "publishedDate": "2024-01-02", "thumbnail": "", "open_group": true},
    {"url": "https://blog.example/timeouts", "title": "Timeouts", "content": "", "engine": "bing"}
  ],
  "answers": [],
  "corrections": [],
  "infoboxes": [],
  "suggestions": [],
  "unresponsive_engines": [["brave", "too many requests"], ["duckduckgo", "CAPTCHA"], ["startpage", "timeout"]]
}`

func TestSearxNGDecodesALiveShape(t *testing.T) {
	srv := searxServer(t, 200, "application/json", searxLiveBody)
	res, err := NewSearxNG(srv.URL, nil, "Strument/test")(context.Background(), "golang http client timeout")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(res.Results))
	}
	if res.Results[0].URL != "https://pkg.go.dev/net/http" || res.Results[0].Published != "2024-01-02" {
		t.Errorf("first result = %+v", res.Results[0])
	}
	// A snippet-less result is normal and must survive rather than be dropped:
	// the URL and title are still the answer to "what is worth reading".
	if res.Results[1].Content != "" || res.Results[1].Title != "Timeouts" {
		t.Errorf("second result = %+v", res.Results[1])
	}
	// Two-element arrays, not objects. Read off webutils.get_json_response and
	// confirmed live; decoding these as objects yields three empty engines and
	// a report that says nothing.
	want := []UnresponsiveEngine{
		{"brave", "too many requests"}, {"duckduckgo", "CAPTCHA"}, {"startpage", "timeout"},
	}
	if len(res.Unresponsive) != 3 || res.Unresponsive[0] != want[0] || res.Unresponsive[2] != want[2] {
		t.Errorf("unresponsive = %+v, want %+v", res.Unresponsive, want)
	}
}

// The three ways a real instance fails, each of which looks like something
// else. All three were seen while designing this; none is hypothetical.
func TestSearxNGTranslatesTheRealFailures(t *testing.T) {
	for _, tc := range []struct {
		name, ctype, body, want string
		status                  int
	}{
		{
			// SearXNG ships `formats: [html]`, so this is what every instance
			// does until an admin opts in. The message is the fix.
			name: "403 because json is not enabled", status: 403,
			ctype: "text/html", body: "<html>forbidden</html>",
			want: "search.formats",
		},
		{
			name: "429 from the limiter", status: 429,
			ctype: "text/html", body: "Too Many Requests",
			want: "rate-limiting",
		},
		{
			// The one that looks like success: HTTP 200, an anti-bot page. Two
			// public instances did exactly this, and a status-only check
			// accepts it — then the decoder blames the JSON.
			name: "200 carrying a bot check", status: 200,
			ctype: "text/html; charset=utf-8", body: "<!doctype html><title>Verifying your browser…</title>",
			want: "bot check",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := searxServer(t, tc.status, tc.ctype, tc.body)
			_, err := NewSearxNG(srv.URL, nil, "")(context.Background(), "q")
			if err == nil {
				t.Fatal("no error at all")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error was %q, want it to mention %q", err, tc.want)
			}
			// Never the raw symptom on its own: "HTTP 403" or "invalid
			// character '<'" points nobody anywhere.
			if strings.Contains(err.Error(), "invalid character") {
				t.Errorf("the decoder's complaint leaked through: %v", err)
			}
		})
	}
}

// Results beyond the tenth are dropped, and a result with no URL is dropped
// wherever it sits: it is a line a reader cannot act on.
func TestSearxNGCapsAndSkipsUnusableResults(t *testing.T) {
	var results []map[string]any
	results = append(results, map[string]any{"url": "", "title": "no url"})
	for i := range 15 {
		results = append(results, map[string]any{
			"url": "https://example.com/" + string(rune('a'+i)), "title": "t", "content": "c",
		})
	}
	body, err := json.Marshal(map[string]any{"query": "q", "results": results})
	if err != nil {
		t.Fatal(err)
	}
	srv := searxServer(t, 200, "application/json", string(body))
	res, err := NewSearxNG(srv.URL, nil, "")(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != searxMaxResults {
		t.Errorf("got %d results, want the cap of %d", len(res.Results), searxMaxResults)
	}
	for _, r := range res.Results {
		if r.URL == "" {
			t.Error("a result with no URL survived")
		}
	}
}

// A snippet is text from whatever page ranked, so nothing about its length is
// the instance's decision. Unbounded, one result could push the other nine —
// and the note about unresponsive engines — past the tool-result cap, which
// trims from the tail. The bound is per field so the total stays small enough
// that nothing is ever cut.
func TestSearxNGBoundsHostileSnippets(t *testing.T) {
	huge := strings.Repeat("A", 200_000)
	body := `{"query":"q","results":[{"url":"https://x.example/1","title":"` + huge +
		`","content":"` + huge + `"}],"unresponsive_engines":[["brave","timeout"]]}`
	srv := searxServer(t, 200, "application/json", body)
	res, err := NewSearxNG(srv.URL, nil, "")(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{res.Results[0].Title, res.Results[0].Content} {
		if n := len([]rune(got)); n > searxMaxSnippet+1 {
			t.Errorf("field kept %d runes, want at most %d", n, searxMaxSnippet+1)
		}
	}
	// And the whole rendered result stays well inside what one tool result
	// carries, so the trailing note cannot be trimmed away.
	if out := formatSearchResults("q", res); len(out) > maxToolOutputBytes {
		t.Errorf("rendered result is %d bytes, past the %d cap", len(out), maxToolOutputBytes)
	} else if !strings.Contains(out, "brave") {
		t.Errorf("the engine note did not survive:\n%s", out[max(0, len(out)-200):])
	}

	// A multi-byte snippet is cut on a rune boundary, not mid-character.
	body = `{"query":"q","results":[{"url":"https://x.example/2","title":"t","content":"` +
		strings.Repeat("日", 5000) + `"}]}`
	srv2 := searxServer(t, 200, "application/json", body)
	res2, err := NewSearxNG(srv2.URL, nil, "")(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(res2.Results[0].Content) {
		t.Error("clipping broke a multi-byte character")
	}
}

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

// exaServer answers like the service, recording what it was sent so a test can
// assert on the request as well as the reply.
func exaServer(t *testing.T, status int, body string) (*httptest.Server, *http.Header, *map[string]any) {
	t.Helper()
	var gotHeader http.Header
	gotBody := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path = %q, want /search", r.URL.Path)
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

// Shaped after a live response, trimmed to the fields used. Note publishedDate
// on some results and not others, text absent on one, and a result with no url
// at all — each of which the reducer has to survive.
const exaLiveBody = `{
  "requestId": "c5347807dc367ef8f7d8dddd0960b645",
  "results": [
    {"title": "wazero", "url": "https://wazero.io/",
     "text": "wazero # the zero dependency WebAssembly runtime for Go developers"},
    {"title": "github.com/tetratelabs/wazero v1.9.0",
     "url": "https://pkg.go.dev/github.com/tetratelabs/wazero@v1.9.0",
     "publishedDate": "2025-12-18T00:00:00.000Z",
     "text": "wazero is a WebAssembly Core Specification 1.0 and 2.0 compliant runtime."},
    {"title": "no text here", "url": "https://example.org/page"},
    {"title": "dropped, no url", "url": "", "text": "invisible"}
  ],
  "costDollars": {"total": 0.007}
}`

func TestExaRequestAndResults(t *testing.T) {
	srv, hdr, body := exaServer(t, http.StatusOK, exaLiveBody)
	res, err := NewExa(srv.URL, "secret-key", nil, "Strument/test")(
		context.Background(), "wazero WebAssembly runtime Go")
	if err != nil {
		t.Fatal(err)
	}

	// The key rides in x-api-key, and nowhere else.
	if got := hdr.Get("X-Api-Key"); got != "secret-key" {
		t.Errorf("x-api-key = %q", got)
	}
	if got := hdr.Get("User-Agent"); got != "Strument/test" {
		t.Errorf("User-Agent = %q, want Strument's own", got)
	}
	if (*body)["query"] != "wazero WebAssembly runtime Go" {
		t.Errorf("query = %v", (*body)["query"])
	}
	if (*body)["numResults"] != float64(searchMaxResults) {
		t.Errorf("numResults = %v, want %d", (*body)["numResults"], searchMaxResults)
	}

	// Three results, the url-less one dropped.
	if len(res.Results) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(res.Results), res.Results)
	}
	if res.Query != "wazero WebAssembly runtime Go" {
		t.Errorf("query = %q", res.Query)
	}
	if res.Results[0].URL != "https://wazero.io/" || !strings.Contains(res.Results[0].Content, "zero dependency") {
		t.Errorf("result 0 = %+v", res.Results[0])
	}
	// publishedDate is carried through unparsed where the service gives one.
	if res.Results[1].Published != "2025-12-18T00:00:00.000Z" {
		t.Errorf("published = %q, want it passed through as given", res.Results[1].Published)
	}
	// And absent where it is not, rather than invented.
	if res.Results[0].Published != "" || res.Results[2].Content != "" {
		t.Errorf("missing fields should stay empty: %+v", res.Results)
	}
	// Exa is one index, not a federation, so it can report no engine health.
	if len(res.Unresponsive) != 0 {
		t.Errorf("Unresponsive = %+v, want empty for a single-index backend", res.Unresponsive)
	}
}

// The search type is sent, and is `fast` rather than the service's own default.
//
// This is the one request field that is a finding rather than a default, so it
// gets its own check: `auto` returns a bare origin in place of the page URL on
// about 18% of results, and a search result's URL is what webfetch is handed
// next. If this silently reverted, the failure would be a model reading the
// wrong page and saying nothing — which no other test here could catch.
func TestExaAsksForFastRatherThanTheDefault(t *testing.T) {
	srv, _, body := exaServer(t, http.StatusOK, exaLiveBody)
	if _, err := NewExa(srv.URL, "k", nil, "")(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if got := (*body)["type"]; got != "fast" {
		t.Errorf("type = %v, want %q — auto returns homepage URLs for deep pages", got, "fast")
	}
}

// Text is requested, because a result with no snippet is a URL a model has to
// spend a fetch on to evaluate.
func TestExaRequestsPageText(t *testing.T) {
	srv, _, body := exaServer(t, http.StatusOK, exaLiveBody)
	if _, err := NewExa(srv.URL, "k", nil, "")(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	contents, ok := (*body)["contents"].(map[string]any)
	if !ok {
		t.Fatalf("contents = %v, want an object requesting text", (*body)["contents"])
	}
	text, ok := contents["text"].(map[string]any)
	if !ok || text["maxCharacters"] != float64(searchMaxSnippet) {
		t.Errorf("contents.text = %v, want maxCharacters %d", contents["text"], searchMaxSnippet)
	}
}

// Each failure the user can act on says what to do, and they differ. The 402 is
// the one that would otherwise arrive as a wall of x402 payment JSON.
func TestExaErrorsNameTheirFix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			"missing key", http.StatusPaymentRequired,
			`{"requestId":"a","error":"Payment required to access this resource","tag":"X402_PAYMENT_REQUIRED"}`,
			"api_key=",
		},
		{
			"bad key", http.StatusUnauthorized,
			`{"requestId":"b","error":"Invalid API key","tag":"INVALID_API_KEY"}`,
			"rejected the API key",
		},
		{
			"rate limited", http.StatusTooManyRequests,
			`{"requestId":"c","error":"Too many requests","tag":"RATE_LIMIT"}`,
			"rate-limiting",
		},
		{
			// Anything else keeps the service's own words and the request id,
			// which is the only handle a user has when asking Exa about it.
			"other", http.StatusBadRequest,
			`{"requestId":"deadbeef","error":"Invalid request body","tag":"INVALID_REQUEST_BODY"}`,
			"deadbeef",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := exaServer(t, tc.status, tc.body)
			_, err := NewExa(srv.URL, "k", nil, "")(context.Background(), "q")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A 200 carrying an error string is still a failure. The envelope reports the
// condition in the body, so status alone is not the check.
func TestExaErrorInABodyWithStatusOK(t *testing.T) {
	srv, _, _ := exaServer(t, http.StatusOK, `{"requestId":"x1","error":"something broke"}`)
	_, err := NewExa(srv.URL, "k", nil, "")(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "something broke") {
		t.Errorf("error = %v, want the body's own message", err)
	}
}

// Bytes that are not JSON report the status, because a proxy or captive portal
// answering instead of the service is the likelier cause than a format change.
func TestExaNonJSONReportsTheStatus(t *testing.T) {
	srv, _, _ := exaServer(t, http.StatusBadGateway, "<html>gateway</html>")
	_, err := NewExa(srv.URL, "k", nil, "")(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %v, want it to name the status", err)
	}
}

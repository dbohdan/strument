package decide

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestParseSystemOne covers the shapes real endpoints return. The answers are
// the ones TypeSafe (through OpenRouter) and Ollaya gave during
// doc/experiments/2026-09-approve-model and its local test; the errors are
// each endpoint's own form. Anything short of a probability for "safe" is an
// error, because an error means the prompt is shown.
func TestParseSystemOne(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    float64
		model   string
		wantErr string
	}{
		{"jev via openrouter", 200,
			`{"model":"typesafe/jev-1.13-20260917","answers":{"decision":{"type":"choice","choice":"safe",` +
				`"probabilities":{"ask":0.02,"safe":0.98},"confidence":0.95}},"usage":{"input_tokens":468,` +
				`"output_tokens":31,"cost":1.9656e-05},"provider":"TypeSafe"}`,
			0.98, "typesafe/jev-1.13-20260917", ""},
		// A router answers as the checkpoint it chose, and there is no cost.
		{"ollaya", 200,
			`{"model":"laya:en","answers":{"decision":{"type":"choice","choice":"ask","confidence":0.4,` +
				`"probabilities":{"safe":0.3,"ask":0.7}}},"usage":{"input_tokens":43,"output_tokens":0}}`,
			0.3, "laya:en", ""},
		// Cloudflare Workers AI wraps the answer in its own envelope. Bodies as
		// the API returned them on 2026-10-02.
		{"clef via cloudflare", 200,
			`{"result":{"model":"clef-flash","answers":{"decision":{"type":"choice","choice":"safe",` +
				`"probabilities":{"safe":0.909,"ask":0.091},"confidence":0.669}},"usage":{"input_tokens":301,` +
				`"output_tokens":0}},"success":true,"errors":[],"messages":[]}`,
			0.909, "clef-flash", ""},
		{"cloudflare auth error", 401, `{"result":null,"success":false,"errors":[{"code":10000,` +
			`"message":"Authentication error"}],"messages":[]}`, 0, "",
			"HTTP 401 from the decision model: Authentication error"},
		{"cloudflare unknown model", 400, `{"success":false,"errors":[{"code":7000,` +
			`"message":"No route for that URI"}],"messages":[],"result":null}`, 0, "",
			"HTTP 400 from the decision model: No route for that URI"},
		{"no answer", 200, `{"model":"m","answers":{}}`, 0, "", "no answer"},
		{"no safe option", 200, `{"answers":{"decision":{"type":"choice","probabilities":{"ask":1}}}}`, 0, "",
			`no probability for "safe"`},
		{"not a probability", 200, `{"answers":{"decision":{"type":"choice","probabilities":{"safe":1.5}}}}`, 0, "",
			"not a probability"},
		{"wrong question type", 200, `{"answers":{"decision":{"type":"noul","noul":0.9}}}`, 0, "", `"noul"`},
		{"openrouter error object", 401, `{"error":{"message":"No auth credentials found","code":401}}`, 0, "",
			"HTTP 401 from the decision model: No auth credentials found"},
		{"ollaya error string", 404, `{"error":"model \"jev-latest:latest\" not found, try pulling it first",` +
			`"code":"MODEL_NOT_FOUND"}`, 0, "", "not found, try pulling it first"},
		// Ollaya after its fix for silent truncation (ollaya-dev/ollaya#16): a
		// state longer than the model reads is refused, and the refusal is a
		// failure, so the prompt is shown.
		{"ollaya refuses a truncated state", 422, `{"error":"state does not fit laya:en's context",` +
			`"code":"STATE_TRUNCATED","detail":[{"loc":["body","state"],"msg":"truncated by laya:en",` +
			`"type":"state_truncated"}]}`, 0, "", "HTTP 422 from the decision model: state does not fit"},
		{"validation detail", 422, `{"detail":[{"loc":["body","state"],"msg":"field required"}]}`, 0, "",
			"field required"},
		{"a proxy's HTML", 502, `<html>Bad Gateway</html>`, 0, "", "HTTP 502"},
		{"garbage on 200", 200, `not json`, 0, "", "valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := parseSystemOne(tc.status, []byte(tc.body))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.PSafe != tc.want || d.Model != tc.model {
				t.Errorf("got p=%v model=%q, want p=%v model=%q", d.PSafe, d.Model, tc.want, tc.model)
			}
		})
	}
}

// TestSystemOneRequest pins what is sent: the slug, the three state fields,
// the tested rubric, and a bearer key only when there is one — a local server
// needs none, and an empty "Bearer " is a malformed header.
func TestSystemOneRequest(t *testing.T) {
	for _, key := range []string{"k-123", ""} {
		var got map[string]any
		var auth, title, referer string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			title, referer = r.Header.Get("X-Title"), r.Header.Get("Http-Referer")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			_, _ = w.Write([]byte(`{"model":"m","answers":{"decision":{"type":"choice","probabilities":{"safe":0.91,"ask":0.09}}}}`))
		}))
		decide := NewSystemOne(srv.URL+"/v1/systemone", "laya", key, nil, nil, "test")
		d, err := decide(context.Background(), Input{Root: "/p", Command: "go test ./...", Purpose: "Run the tests"})
		srv.Close()
		if err != nil || d.PSafe != 0.91 {
			t.Fatalf("d=%+v err=%v", d, err)
		}
		if (key == "") != (auth == "") || (key != "" && auth != "Bearer "+key) {
			t.Errorf("key %q sent Authorization %q", key, auth)
		}
		// OpenRouter lists a request without these as app "Unknown".
		if title != "Strument" || referer == "" {
			t.Errorf("attribution headers X-Title=%q HTTP-Referer=%q; want Strument's", title, referer)
		}
		state, _ := got["state"].(map[string]any)
		if got["model"] != "laya" || state["project_root"] != "/p" || state["command"] != "go test ./..." ||
			state["purpose_written_by_the_agent"] != "Run the tests" {
			t.Errorf("body = %v", got)
		}
		q, _ := got["questions"].(map[string]any)["decision"].(map[string]any)
		crit, _ := q["criteria"].(map[string]any)
		if q["type"] != "choice" || crit["safe"] != approveSafeText || crit["ask"] != approveAskText {
			t.Errorf("question = %v, want the tested rubric", q)
		}
	}
}

// TestApproveRubricIsTheOneEvaluated: the rubric's pass is evidence about
// its exact text, so the constants here must match the eval's runner word for
// word. Editing either without the other is how a shipped classifier would
// quietly become an untested one.
func TestApproveRubricIsTheOneEvaluated(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "doc", "experiments", "2026-09-approve-model", "data", "run.py"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// pyString joins the adjacent literals of NAME = ( "…" "…" ).
	pyString := func(name string) string {
		i := strings.Index(src, name+" = (")
		if i < 0 {
			t.Fatalf("run.py has no %s", name)
		}
		block := src[i : i+strings.Index(src[i:], "\n)")]
		var b strings.Builder
		for _, m := range regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(block, -1) {
			b.WriteString(m[1])
		}
		return b.String()
	}
	if got := pyString("SAFE_TEXT"); got != approveSafeText {
		t.Errorf("safe text differs from the eval's:\n go: %q\npy: %q", approveSafeText, got)
	}
	if got := pyString("ASK_TEXT"); got != approveAskText {
		t.Errorf("ask text differs from the eval's:\n go: %q\npy: %q", approveAskText, got)
	}
	i := strings.Index(src, `"instructions": (`)
	if i < 0 {
		t.Fatal("run.py's D1 has no instructions")
	}
	var b strings.Builder
	for _, m := range regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(src[i+len(`"instructions": (`):i+strings.Index(src[i:], "),")], -1) {
		b.WriteString(m[1])
	}
	if b.String() != approveInstructions {
		t.Errorf("instructions differ from the eval's:\n go: %q\npy: %q", approveInstructions, b.String())
	}
}

// The config's headers reach the decision request after Strument's own: the
// bearer key stays, and a configured User-Agent replaces the default.
func TestSystemOneConfigHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"model":"m","answers":{"decision":{"type":"choice","probabilities":{"safe":0.5,"ask":0.5}}}}`))
	}))
	defer srv.Close()
	d := NewSystemOne(srv.URL, "laya", "k", map[string]string{"x-cmd-zdr": "1", "user-agent": "custom/1"}, nil, "test")
	if _, err := d(context.Background(), Input{Command: "ls"}); err != nil {
		t.Fatal(err)
	}
	if got.Get("X-Cmd-Zdr") != "1" || got.Get("User-Agent") != "custom/1" || got.Get("Authorization") != "Bearer k" {
		t.Errorf("headers = %v", got)
	}
}

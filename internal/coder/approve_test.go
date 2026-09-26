package coder

import (
	"context"
	"encoding/json"
	"errors"
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
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			_, _ = w.Write([]byte(`{"model":"m","answers":{"decision":{"type":"choice","probabilities":{"safe":0.91,"ask":0.09}}}}`))
		}))
		decide := NewSystemOne(srv.URL+"/v1/systemone", "laya", key, nil, "test")
		d, err := decide(context.Background(), DecisionInput{Root: "/p", Command: "go test ./...", Purpose: "Run the tests"})
		srv.Close()
		if err != nil || d.PSafe != 0.91 {
			t.Fatalf("d=%+v err=%v", d, err)
		}
		if (key == "") != (auth == "") || (key != "" && auth != "Bearer "+key) {
			t.Errorf("key %q sent Authorization %q", key, auth)
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

// fixedDecider answers every command the same way and counts the calls.
type fixedDecider struct {
	p     float64
	err   error
	calls int
}

func (f *fixedDecider) decide(context.Context, DecisionInput) (Decision, error) {
	f.calls++
	if f.err != nil {
		return Decision{}, f.err
	}
	return Decision{PSafe: f.p, Model: "laya:en"}, nil
}

// TestApproveModelStandsInForThePromptOnly drives runShell. The model is asked
// only when a prompt would really be shown and the sandbox is enforcing; an
// approval skips the prompt; anything else shows it; and every call leaves a
// decision record with its p(safe).
func TestApproveModelStandsInForThePromptOnly(t *testing.T) {
	unixOnly(t)
	for _, tc := range []struct {
		name       string
		p          float64
		err        error
		sandbox    bool
		granted    bool
		wantCalls  int
		wantPrompt bool
		outcome    string
		line       string
	}{
		{"approved", 0.95, nil, true, false, 1, false, "approved", "Approved by laya:en, p(safe) 0.95:"},
		{"at the threshold", 0.9, nil, true, false, 1, false, "approved", "p(safe) 0.90"},
		{"below", 0.5, nil, true, false, 1, true, "asked", "Not approved by laya:en, p(safe) 0.50 < 0.90:"},
		{"failed", 0, errors.New("connection refused"), true, false, 1, true, "failed", "did not answer"},
		{"no sandbox", 0.99, nil, false, false, 0, true, "", ""},
		{"already granted", 0.99, nil, true, true, 0, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCoder(t)
			out := &captureOut{}
			c.Out = out
			rec := &capture{}
			c.Recorder = rec
			rc := &recordingConfirmer{answer: true}
			c.Confirm = rc
			c.SuggestShellCommands = true
			c.Sandbox = SandboxState{Active: tc.sandbox}
			c.Grants = NewGrants(map[string]bool{GrantBash: tc.granted})
			if tc.granted {
				c.Confirm = AutoConfirmer{Granted: c.Grants.Effective, Fallback: rc}
			}
			fd := &fixedDecider{p: tc.p, err: tc.err}
			c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}

			_, ran := c.runShell(context.Background(), toolCommand{command: "echo hi", purpose: "test"})
			if !ran {
				t.Fatal("the command did not run")
			}
			if fd.calls != tc.wantCalls {
				t.Errorf("decision model called %d times, want %d", fd.calls, tc.wantCalls)
			}
			if prompted := len(rc.got) > 0; prompted != tc.wantPrompt {
				t.Errorf("prompted = %v, want %v", prompted, tc.wantPrompt)
			}
			joined := strings.Join(out.lines, "\n")
			if tc.line != "" && !strings.Contains(joined, tc.line) {
				t.Errorf("output lacks %q:\n%s", tc.line, joined)
			}
			var dec []Record
			for _, r := range rec.recs {
				if r.Type == "decision" {
					dec = append(dec, r)
				}
			}
			if tc.outcome == "" {
				if len(dec) != 0 {
					t.Errorf("recorded %d decisions for a call never made", len(dec))
				}
				return
			}
			if len(dec) != 1 || dec[0].Outcome != tc.outcome {
				t.Fatalf("decision records = %+v, want one with outcome %q", dec, tc.outcome)
			}
			if (tc.err == nil) != (dec[0].PSafe != nil) {
				t.Errorf("p_safe recorded = %v; it belongs on an answer and not on a failure", dec[0].PSafe)
			}
		})
	}
}

// TestApproveModelNeverOverridesADecline: below the threshold the prompt
// decides, and a "no" there is a no.
func TestApproveModelNeverOverridesADecline(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	c.Confirm = noConfirmer{}
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	fd := &fixedDecider{p: 0.2}
	c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}
	if _, ran := c.runShell(context.Background(), toolCommand{command: "echo hi", purpose: "test"}); ran {
		t.Error("a declined command ran")
	}
}

// TestApproveModelSkipsLongCommands: a command too long to be read whole is
// asked about without consulting the model, since a model that cut it would
// answer about a prefix and could not say so.
func TestApproveModelSkipsLongCommands(t *testing.T) {
	c := testCoder(t)
	out := &captureOut{}
	c.Out = out
	rc := &recordingConfirmer{answer: false}
	c.Confirm = rc
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	fd := &fixedDecider{p: 1}
	c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}
	long := strings.Repeat("echo ok; ", 100) + "rm -rf ~/.ssh"
	if _, ran := c.runShell(context.Background(), toolCommand{command: long, purpose: "checks"}); ran {
		t.Error("a declined long command ran")
	}
	if fd.calls != 0 || len(rc.got) != 1 {
		t.Errorf("decider calls %d, prompts %d; want 0 and 1", fd.calls, len(rc.got))
	}
	if !strings.Contains(strings.Join(out.lines, "\n"), "more than it may read whole") {
		t.Errorf("no line said why the model was not asked: %v", out.lines)
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

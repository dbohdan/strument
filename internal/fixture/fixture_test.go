package fixture

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dbohdan/strument/internal/llm"
)

const sample = `{"v":1,"kind":"meta","scenario":"edit-success","source":"authored","model":"deepseek/deepseek-v4-flash"}
{"kind":"fs","path":"main.go","content":"package main\n"}
{"kind":"git","mode":"none"}
{"kind":"chat","editable":["main.go"],"readonly":[]}
{"kind":"user","text":"add a hello function"}
{"kind":"confirm","prompt":"Run shell command?","answer":"y"}
{"kind":"command","block":"go test ./...","exit":0,"output":"ok\n"}
{"kind":"request","body":{"model":"m","stream":true},"assert":"subset","ignore":["tools"]}
{"kind":"stream","events":[{"kind":"Reasoning","text":"hmm"},{"kind":"Answer","text":"hi"},{"kind":"Finish","finish_reason":"stop"},{"kind":"Usage","usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.0001}}]}
{"kind":"expect_fs","path":"main.go","content":"package main\n\nfunc hello() {}\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_history","messages":[{"role":"user","text":"add a hello function"}]}
{"kind":"expect_usage","sent":10,"received":2,"cost_known":true,"usd":0.0001}
`

func TestReadScenario(t *testing.T) {
	sc, err := Read(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Meta.Scenario != "edit-success" || sc.Meta.Source != "authored" {
		t.Errorf("meta = %+v", sc.Meta)
	}
	if len(sc.FS) != 1 || sc.FS[0].Path != "main.go" {
		t.Errorf("fs = %+v", sc.FS)
	}
	if sc.Git == nil || sc.Git.Mode != "none" {
		t.Errorf("git = %+v", sc.Git)
	}
	if sc.User != "add a hello function" {
		t.Errorf("user = %q", sc.User)
	}
	if len(sc.Turns) != 1 {
		t.Fatalf("turns = %d", len(sc.Turns))
	}
	turn := sc.Turns[0]
	if turn.Request == nil || turn.Request.Assert != "subset" {
		t.Errorf("request = %+v", turn.Request)
	}
	if len(turn.Events) != 4 || turn.Events[3].Usage.PromptTokens != 10 {
		t.Errorf("events = %+v", turn.Events)
	}
	if sc.ExpectOutcome.Outcome != "Success" {
		t.Errorf("outcome = %+v", sc.ExpectOutcome)
	}
	if !sc.ExpectUsage.CostKnown || sc.ExpectUsage.USD != 0.0001 {
		t.Errorf("usage = %+v", sc.ExpectUsage)
	}
}

func TestVersionMismatchFailsLoudly(t *testing.T) {
	_, err := Read(strings.NewReader(`{"v":2,"kind":"meta","scenario":"x","source":"authored"}`))
	if err == nil || !strings.Contains(err.Error(), "schema v2") {
		t.Fatalf("want version mismatch error, got %v", err)
	}
}

func TestFirstRowMustBeMeta(t *testing.T) {
	_, err := Read(strings.NewReader(`{"kind":"fs","path":"a","content":""}`))
	if err == nil || !strings.Contains(err.Error(), "must be meta") {
		t.Fatalf("want meta-first error, got %v", err)
	}
}

func TestUnknownKindFailsLoudly(t *testing.T) {
	_, err := Read(strings.NewReader(
		`{"v":1,"kind":"meta","scenario":"x","source":"authored"}` + "\n" +
			`{"kind":"surprise"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown row kind") {
		t.Fatalf("want unknown-kind error, got %v", err)
	}
}

func TestStreamStubReplaysAndErrors(t *testing.T) {
	sc, err := Read(strings.NewReader(
		`{"v":1,"kind":"meta","scenario":"x","source":"authored"}` + "\n" +
			`{"kind":"stream","events":[{"kind":"Answer","text":"partial"},{"kind":"Error","class":"network","message":"connection reset"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	stub := NewStreamStub(sc)
	var texts []string
	var gotErr error
	for ev, err := range stub.Send(context.Background(), llm.Request{}) {
		if err != nil {
			gotErr = err
			break
		}
		texts = append(texts, ev.Text)
	}
	if len(texts) != 1 || texts[0] != "partial" {
		t.Errorf("texts = %v", texts)
	}
	se := &llm.StreamError{}
	ok := errors.As(gotErr, &se)
	if !ok || se.Class != llm.ErrNetwork || !se.Retryable() {
		t.Errorf("err = %v", gotErr)
	}
	// A second Send has no turn to serve.
	for _, err := range stub.Send(context.Background(), llm.Request{}) {
		if err == nil {
			t.Fatal("want error on exhausted turns")
		}
		break
	}
}

func TestConfirmAndCommandScripts(t *testing.T) {
	sc, err := Read(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	conf := NewConfirmScript(sc)
	ans, err := conf.Ask("Run shell command?")
	if err != nil || ans != "y" {
		t.Errorf("ask = %q, %v", ans, err)
	}
	if _, err := conf.Ask("again?"); err == nil {
		t.Error("want error on unscripted prompt")
	}
	cmd := NewCommandScript(sc)
	exit, out, err := cmd.Run("go test ./...")
	if err != nil || exit != 0 || out != "ok\n" {
		t.Errorf("run = %d, %q, %v", exit, out, err)
	}
	if _, _, err := cmd.Run("rm -rf /"); err == nil {
		t.Error("want error on unscripted command")
	}
}

func TestLoadCapturedSmokeFixture(t *testing.T) {
	sc, err := Load("../../testdata/fixtures/basecoder/edit-success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Meta.Source != "captured" || sc.Meta.AiderSHA == "" {
		t.Errorf("meta = %+v", sc.Meta)
	}
	if len(sc.Turns) != 1 || sc.Turns[0].Request == nil {
		t.Fatalf("want 1 turn with request, got %d", len(sc.Turns))
	}
	var answer strings.Builder
	stub := NewStreamStub(sc)
	sawUsage := false
	for ev, err := range stub.Send(context.Background(), llm.Request{}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Kind {
		case llm.EventAnswer:
			answer.WriteString(ev.Text)
		case llm.EventUsage:
			sawUsage = true
			if ev.Usage.Cost == nil || *ev.Usage.Cost == 0 {
				t.Error("captured fixture should carry in-band cost")
			}
		}
	}
	if !strings.Contains(answer.String(), "<<<<<<< SEARCH") {
		t.Errorf("assembled answer lacks an edit block:\n%s", answer.String())
	}
	if !sawUsage {
		t.Error("no usage event replayed")
	}
}

// Record/replay driver: fixture scenarios drive the coder end-to-end with
// no network (basecoder-spec §11, fixture-harness-spec).

package coder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbohdan/strument/internal/config"
	"github.com/dbohdan/strument/internal/fixture"
	"github.com/dbohdan/strument/internal/llm"
)

// --- fixture stub adapters ---

type scriptConfirmer struct {
	t      *testing.T
	script *fixture.ConfirmScript
}

func (s scriptConfirmer) Confirm(req ConfirmRequest) (bool, bool) {
	ans, err := s.script.Ask(req.Prompt)
	if err != nil {
		s.t.Fatalf("confirm: %v", err)
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "y", "yes", "a", "all":
		return true, false
	case "never", "d":
		return false, true
	default:
		return false, false
	}
}

type scriptRunner struct {
	t      *testing.T
	script *fixture.CommandScript
}

func (s scriptRunner) Run(_ context.Context, block, _ string) (int, string, error) {
	exit, out, err := s.script.Run(block)
	if err != nil {
		s.t.Fatalf("command: %v", err)
	}
	return exit, out, nil
}

type testOutput struct{ t *testing.T }

func (o testOutput) Printf(format string, args ...any)   { o.t.Logf("out: "+format, args...) }
func (o testOutput) Warningf(format string, args ...any) { o.t.Logf("warn: "+format, args...) }
func (o testOutput) Errorf(format string, args ...any)   { o.t.Logf("err: "+format, args...) }
func (o testOutput) StreamText(string)                   {}
func (o testOutput) StreamReasoning(string)              {}
func (o testOutput) FlushStream()                        {}

// replayEnv is one scenario wired up and ready to run.
type replayEnv struct {
	sc    *fixture.Scenario
	coder *Coder
	stub  *fixture.StreamStub
	dir   string
}

// setupScenario materializes the fixture fs in a temp root and builds the
// coder against the replay stubs.
func setupScenario(t *testing.T, sc *fixture.Scenario, mutate func(*Coder)) *replayEnv {
	t.Helper()
	dir := t.TempDir()
	for _, f := range sc.FS {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	slug := sc.Meta.Model
	if slug == "" {
		slug = "test-model"
	}
	model := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       slug,
		EditFormat: "diff",
		RepoMap:    false,
	}
	model.WeakModel = model

	c := New(dir, model)
	stub := fixture.NewStreamStub(sc)
	c.Client = stub
	c.Confirm = scriptConfirmer{t, fixture.NewConfirmScript(sc)}
	c.Runner = scriptRunner{t, fixture.NewCommandScript(sc)}
	c.Out = testOutput{t}
	c.Clock = RealClock{} // replay streams never sleep long; retries use Clock below
	if sc.Chat != nil {
		for _, f := range sc.Chat.Editable {
			c.AddFile(f)
		}
		for _, f := range sc.Chat.Readonly {
			c.AddReadOnlyFile(f)
		}
	}
	if mutate != nil {
		mutate(c)
	}
	return &replayEnv{sc: sc, coder: c, stub: stub, dir: dir}
}

// run executes the scenario's user message and asserts the expect rows.
func (env *replayEnv) run(t *testing.T) {
	t.Helper()
	env.coder.Run(context.Background(), env.sc.User)
	env.assertExpectations(t)
}

func (env *replayEnv) assertExpectations(t *testing.T) {
	t.Helper()
	sc, c := env.sc, env.coder

	for _, f := range sc.ExpectFS {
		data, err := os.ReadFile(filepath.Join(env.dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("expect_fs %s: %v", f.Path, err)
			continue
		}
		if string(data) != f.Content {
			t.Errorf("expect_fs %s:\nwant %q\n got %q", f.Path, f.Content, string(data))
		}
	}

	if e := sc.ExpectOutcome; e != nil {
		if c.lastSendOutcome.String() != e.Outcome {
			t.Errorf("outcome = %s, want %s", c.lastSendOutcome, e.Outcome)
		}
		if c.numReflections != e.Reflections {
			t.Errorf("reflections = %d, want %d", c.numReflections, e.Reflections)
		}
	}

	if len(sc.ExpectHistory) > 0 {
		history := append(append([]llm.Message(nil), c.doneMessages...), c.curMessages...)
		if len(history) != len(sc.ExpectHistory) {
			t.Fatalf("history length = %d, want %d\nhistory: %s", len(history), len(sc.ExpectHistory), dumpHistory(history))
		}
		for i, want := range sc.ExpectHistory {
			if history[i].Role != want.Role || history[i].Text() != want.Text {
				t.Errorf("history[%d]:\nwant %s %q\n got %s %q", i, want.Role, want.Text, history[i].Role, history[i].Text())
			}
		}
	}

	if e := sc.ExpectUsage; e != nil {
		if c.totalTokensSent != e.Sent || c.totalTokensReceived != e.Received {
			t.Errorf("usage = %d sent %d received, want %d/%d",
				c.totalTokensSent, c.totalTokensReceived, e.Sent, e.Received)
		}
		if c.sessionKnown != e.CostKnown {
			t.Errorf("costKnown = %v, want %v", c.sessionKnown, e.CostKnown)
		}
		if e.CostKnown && !almostEqual(c.totalCost, e.USD) {
			t.Errorf("cost = %v, want %v", c.totalCost, e.USD)
		}
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func dumpHistory(msgs []llm.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		text := m.Text()
		if len(text) > 80 {
			text = text[:80] + "..."
		}
		b.WriteString(strings.ReplaceAll(strings.TrimRight(
			strings.Join([]string{"\n", string(rune('0' + i%10)), " ", m.Role, ": ", text}, ""), "\n"), "\n", "\\n"))
	}
	return b.String()
}

func loadScenario(t *testing.T, name string) *fixture.Scenario {
	t.Helper()
	sc, err := fixture.Load(filepath.Join("..", "..", "testdata", "fixtures", "basecoder", name))
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func inlineScenario(t *testing.T, jsonl string) *fixture.Scenario {
	t.Helper()
	sc, err := fixture.Read(strings.NewReader(strings.TrimSpace(jsonl)))
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// capturePlatform pins PlatformInfo to the environment of the live capture
// so assembled prompts byte-match the recorded request.
func capturePlatform() PlatformInfo {
	return PlatformInfo{
		Platform: "Linux-6.18.5-x86_64-with-glibc2.39",
		ShellVar: "SHELL",
		ShellVal: "/bin/bash",
		Language: "English",
		Date:     "2026-07-16",
		InGit:    false,
	}
}

// TestReplayEditSuccess replays the captured smoke scenario end-to-end and
// asserts the assembled request against aider's real request (fixture-
// harness §3: parsed-JSON subset, message content not normalized).
func TestReplayEditSuccess(t *testing.T) {
	sc := loadScenario(t, "edit-success.jsonl")
	temp := 0.0
	env := setupScenario(t, sc, func(c *Coder) {
		c.Platform = capturePlatform()
		c.Model.Temperature = &temp
	})

	env.stub.OnRequest = func(turn int, req llm.Request, captured *fixture.Request) error {
		if captured == nil || captured.Assert != "subset" {
			return nil
		}
		assertRequestSubset(t, turn, req, captured)
		return nil
	}

	env.run(t)
}

// assertRequestSubset compares model/stream/temperature and every message's
// role+content against the captured body, printing a focused diff.
func assertRequestSubset(t *testing.T, turn int, req llm.Request, captured *fixture.Request) {
	t.Helper()
	var body struct {
		Model       string   `json:"model"`
		Temperature *float64 `json:"temperature"`
		Messages    []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(captured.Body, &body); err != nil {
		t.Fatalf("turn %d: captured body: %v", turn, err)
	}
	if req.Model != body.Model {
		t.Errorf("turn %d: model = %q, want %q", turn, req.Model, body.Model)
	}
	if body.Temperature != nil {
		if req.Temperature == nil || *req.Temperature != *body.Temperature {
			t.Errorf("turn %d: temperature = %v, want %v", turn, req.Temperature, *body.Temperature)
		}
	}
	if len(req.Messages) != len(body.Messages) {
		var roles []string
		for _, m := range req.Messages {
			roles = append(roles, m.Role)
		}
		t.Fatalf("turn %d: %d messages, want %d (roles: %v)", turn, len(req.Messages), len(body.Messages), roles)
	}
	for i := range body.Messages {
		if req.Messages[i].Role != body.Messages[i].Role {
			t.Errorf("turn %d msg %d: role %q, want %q", turn, i, req.Messages[i].Role, body.Messages[i].Role)
			continue
		}
		got := req.Messages[i].Text()
		want := body.Messages[i].Content
		if got != want {
			t.Errorf("turn %d msg %d (%s): first diff at %q", turn, i, req.Messages[i].Role, firstDiff(got, want))
		}
	}
}

// firstDiff shows the first differing region between two strings.
func firstDiff(got, want string) string {
	n := min(len(got), len(want))
	i := 0
	for i < n && got[i] == want[i] {
		i++
	}
	lo := max(0, i-40)
	gEnd := min(len(got), i+80)
	wEnd := min(len(want), i+80)
	return "got ..." + got[lo:gEnd] + "... want ..." + want[lo:wEnd] + "..."
}

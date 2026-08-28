// Record/replay driver: fixture scenarios drive the coder end-to-end with
// no network.

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

// --- fixture stub adapters ---

type scriptConfirmer struct {
	t      *testing.T
	script *fixture.ConfirmScript
}

func (s scriptConfirmer) Confirm(req ConfirmRequest) ConfirmResult {
	ans, err := s.script.Ask(req.Prompt)
	if err != nil {
		s.t.Fatalf("confirm: %v", err)
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "y", "yes":
		return ConfirmResult{Yes: true}
	case "a", "all":
		return ConfirmResult{Always: true}
	default:
		return ConfirmResult{}
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

// scriptAsker adapts the fixture's ask rows to the coder's Asker. The raw
// scripted line goes through ParseAskAnswer, so a scripted "1" resolves to the
// same label a live user's "1" would — a replay must not interpret answers
// differently from the session it stands in for.
type scriptAsker struct {
	t      *testing.T
	script *fixture.AskScript
}

func (s scriptAsker) Ask(req AskRequest) []string {
	line, err := s.script.Answer(req.Question)
	if err != nil {
		s.t.Fatalf("ask: %v", err)
	}
	return ParseAskAnswer(req, line)
}

type testOutput struct{ t *testing.T }

func (o testOutput) Printf(format string, args ...any)   { o.t.Logf("out: "+format, args...) }
func (o testOutput) Toolf(format string, args ...any)    { o.t.Logf("tool: "+format, args...) }
func (o testOutput) Warningf(format string, args ...any) { o.t.Logf("warn: "+format, args...) }
func (o testOutput) Errorf(format string, args ...any)   { o.t.Logf("err: "+format, args...) }
func (o testOutput) StreamText(string)                   {}
func (o testOutput) StreamReasoning(string)              {}
func (o testOutput) StreamToolCall(int, string, string)  {}
func (o testOutput) FlushStream()                        {}

// replayEnv is one scenario wired up and ready to run.
type replayEnv struct {
	sc       *fixture.Scenario
	coder    *Coder
	stub     *fixture.StreamStub
	confirms *fixture.ConfirmScript
	asks     *fixture.AskScript
	commands *fixture.CommandScript
	dir      string
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
		EditFormat: "tool",
		RepoMap:    false,
	}
	model.SideModel = model

	c := New(dir, model)
	stub := fixture.NewStreamStub(sc)
	c.Client = stub
	confirms := fixture.NewConfirmScript(sc)
	asks := fixture.NewAskScript(sc)
	commands := fixture.NewCommandScript(sc)
	c.Confirm = scriptConfirmer{t, confirms}
	c.Asker = scriptAsker{t, asks}
	c.Runner = scriptRunner{t, commands}
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
	return &replayEnv{sc: sc, coder: c, stub: stub, confirms: confirms, asks: asks, commands: commands, dir: dir}
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

	// An unconsumed scripted row means the scenario expected a prompt or a
	// command that never happened — exactly the drift that should fail loudly
	// rather than pass in silence. (It did pass in silence: a "Create new file?"
	// row outlived the confirmation it scripted.)
	if n := env.confirms.Remaining(); n != 0 {
		t.Errorf("%d scripted confirm row(s) went unused; the run asked fewer questions than the scenario expected", n)
	}
	if n := env.asks.Remaining(); n != 0 {
		t.Errorf("%d scripted ask row(s) went unused", n)
	}
	if n := env.commands.Remaining(); n != 0 {
		t.Errorf("%d scripted command row(s) went unused", n)
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

func inlineScenario(t *testing.T, jsonl string) *fixture.Scenario {
	t.Helper()
	sc, err := fixture.Read(strings.NewReader(strings.TrimSpace(jsonl)))
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// history is the whole conversation, wherever it currently lives. done and cur
// are adjacent on the wire and the split is bookkeeping — a turn's messages
// settle into done when it ends — so a test asking "what does the conversation
// look like" should not have to know which slice holds them.
func history(c *Coder) []llm.Message {
	return append(append([]llm.Message(nil), c.doneMessages...), c.curMessages...)
}

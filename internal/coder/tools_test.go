package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

// toolMode sets the coder to the "tool" edit format for a replay.
func toolMode(c *Coder) { c.editFormat = "tool" }

// lastToolResult returns the final tool-result message in the coder's history.
// With the loop closed, a turn's tool results are followed by the model's
// closing answer, so counting back from the end of the history is not stable;
// every tool scenario asserts through this instead.
func lastToolResult(t *testing.T, c *Coder) llm.Message {
	t.Helper()
	history := append(append([]llm.Message(nil), c.doneMessages...), c.curMessages...)
	for _, m := range slices.Backward(history) {
		if m.Role == llm.RoleTool {
			return m
		}
	}
	t.Fatalf("no tool result in history: %v", history)
	return llm.Message{}
}

// closingTurn is the stream that ends a turn: the model reports what it did and
// calls no tool. Every tool scenario needs one, because a turn whose last
// message is a tool call is mid-sentence and the harness re-sends on the
// results.
const closingTurn = `{"kind":"stream","events":[{"kind":"Answer","text":"Done."},{"kind":"Finish","finish_reason":"stop"}]}`

// TestToolRequestCarriesTools asserts the outgoing request advertises the
// tool set with tool_choice "auto", and that bash is gated on
// whether shell commands are enabled.
func TestToolRequestCarriesTools(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-request-schema","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"hi"}
{"kind":"stream","events":[{"kind":"Answer","text":"hello"},{"kind":"Finish","finish_reason":"stop"}]}
`)
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.SuggestShellCommands = false
	})
	var names []string
	var choice string
	env.stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		choice = req.ToolChoice
		for _, td := range req.Tools {
			names = append(names, td.Name)
		}
		return nil
	}
	env.coder.Run(t.Context(), sc.User)

	if choice != "auto" {
		t.Errorf("tool_choice = %q, want auto", choice)
	}
	// The read-only tools always come first; bash is gated off here, and no
	// verify is configured.
	want := []string{"read", "grep", "glob", "ls", "edit", "write"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v (bash gated off)", names, want)
	}
}

// TestToolSetGating covers the two conditional parts of the tool set: bash
// appears only when shell commands are enabled, verify only when the project
// configured checks, and ask mode offers nothing that changes anything.
func TestToolSetGating(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Coder)
		want   []string
	}{
		{
			"shell enabled, no verify configured",
			func(c *Coder) { c.editFormat = "tool"; c.SuggestShellCommands = true },
			[]string{"read", "grep", "glob", "ls", "edit", "write", "bash"},
		},
		{
			"verify configured",
			func(c *Coder) {
				c.editFormat = "tool"
				c.SuggestShellCommands = true
				c.Verify = []config.VerifyCheck{{Name: "test", Argv: []string{"go", "test", "./..."}}}
			},
			[]string{"read", "grep", "glob", "ls", "edit", "write", "bash", "verify"},
		},
		{
			"ask mode is read-only",
			func(c *Coder) { c.editFormat = "ask"; c.SuggestShellCommands = true },
			[]string{"read", "grep", "glob", "ls"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Coder{}
			tc.mutate(c)
			var names []string
			for _, td := range c.toolDefs() {
				names = append(names, td.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("tools = %v, want %v", names, tc.want)
			}
		})
	}
}

// TestVerifyToolListsChecksInDescription confirms the schema tells the model
// which names exist and what each one runs — the tool takes a name and never a
// command, so the description is the only place that information can live.
func TestVerifyToolListsChecksInDescription(t *testing.T) {
	def := verifyTool([]config.VerifyCheck{
		{Name: "lint", Argv: []string{"golangci-lint", "run"}},
		{Name: "test", Argv: []string{"go", "test", "./..."}},
	})
	for _, want := range []string{"lint: golangci-lint run", "test: go test ./..."} {
		if !strings.Contains(def.Description, want) {
			t.Errorf("description missing %q:\n%s", want, def.Description)
		}
	}
	props, _ := def.Parameters["properties"].(map[string]any)
	nameProp, _ := props["name"].(map[string]any)
	enum, _ := nameProp["enum"].([]any)
	if len(enum) != 2 || enum[0] != "lint" || enum[1] != "test" {
		t.Errorf("name enum = %v, want the configured names in order", enum)
	}
}

// committingRepo is a Repo whose Commit succeeds with a fixed hash, so the
// auto-commit path (and its message) can be asserted.
type committingRepo struct{ tracked []string }

func (r *committingRepo) Root() string             { return "" }
func (r *committingRepo) TrackedFiles() []string   { return r.tracked }
func (r *committingRepo) PathInRepo(_ string) bool { return true }
func (r *committingRepo) IsDirty(_ string) bool    { return false }
func (r *committingRepo) GitIgnored(_ string) bool { return false }
func (r *committingRepo) HeadSHA() string          { return "deadbeef" }
func (r *committingRepo) Commit([]string, string, bool) (string, string, bool, error) {
	return "abc1234", "feat: change world to mars", true, nil
}

// TestToolEditApplies replays a single edit tool call and asserts
// the edit lands on disk, the outcome is a clean success, and the history is
// well-formed: user, assistant-with-tool-call, tool-result.
func TestToolEditApplies(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-edit","source":"authored"}
{"kind":"fs","path":"a.txt","content":"hello\nworld\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"change world to mars"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"world\\n\",\"new_string\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_history","messages":[{"role":"user","text":"change world to mars"},{"role":"assistant","text":""},{"role":"tool","text":"I applied your changes to the files."},{"role":"assistant","text":"Done."}]}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	// The assistant message must carry the tool call, and the tool result must
	// answer its id — otherwise the next send is a malformed request.
	history := env.coder.doneMessages
	assistant := history[1]
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant tool calls = %+v, want one call_1", assistant.ToolCalls)
	}
	if history[2].ToolCallID != "call_1" {
		t.Errorf("tool result answers %q, want call_1", history[2].ToolCallID)
	}
}

// TestToolCreateFile replays a write call and asserts the new file is
// written with its contents.
func TestToolCreateFile(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-create","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"add a greeting file"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"write","tool_args":"{\"path\":\"hi.txt\",\"content\":\"hi there\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"hi.txt","content":"hi there\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)
}

// TestToolCreateFileOverwrites confirms write on an existing path
// replaces the whole file (not the old silent append) and tells the model it
// overwrote, so it doesn't assume the prior contents survived.
func TestToolCreateFileOverwrites(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-overwrite","source":"authored"}
{"kind":"fs","path":"cfg.txt","content":"old line 1\nold line 2\n"}
{"kind":"chat","editable":["cfg.txt"]}
{"kind":"user","text":"rewrite cfg.txt from scratch"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"write","tool_args":"{\"path\":\"cfg.txt\",\"content\":\"brand new\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"cfg.txt","content":"brand new\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	result := lastToolResult(t, env.coder)
	if !strings.Contains(result.Text(), "Overwrote") {
		t.Errorf("result = %q, want it to note the overwrite", result.Text())
	}
}

// TestToolEditCommits asserts that with git auto-commit on, the applied
// edit's tool result names the commit hash — the direct-apply safety net.
func TestToolEditCommits(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-commit","source":"authored"}
{"kind":"fs","path":"a.txt","content":"hello\nworld\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"change world to mars"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"world\\n\",\"new_string\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.AutoCommits = true
		c.Repo = &committingRepo{tracked: []string{"a.txt"}}
	})
	env.run(t)

	result := lastToolResult(t, env.coder).Text()
	if !strings.Contains(result, "abc1234") {
		t.Errorf("tool result = %q, want it to name commit abc1234", result)
	}
}

// TestToolSuggestCommand replays a bash call: the user confirms,
// the command runs, and its output comes back as the tool result.
func TestToolSuggestCommand(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-command","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"run the tests"}
{"kind":"confirm","prompt":"Run shell command?","answer":"yes"}
{"kind":"command","block":"go test ./...","exit":0,"output":"ok\n"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"bash","tool_args":"{\"command\":\"go test ./...\",\"purpose\":\"run the tests\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	result := lastToolResult(t, env.coder)
	if result.ToolCallID != "call_1" {
		t.Fatalf("tool result answers %q, want call_1", result.ToolCallID)
	}
	if !strings.Contains(result.Text(), "Exit status: 0") || !strings.Contains(result.Text(), "ok") {
		t.Errorf("command result = %q, want it to carry exit status and output", result.Text())
	}
}

// TestToolSuggestCommandDeclined confirms a declined command reports back
// without running.
func TestToolSuggestCommandDeclined(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-command-no","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"run the tests"}
{"kind":"confirm","prompt":"Run shell command?","answer":"no"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"bash","tool_args":"{\"command\":\"rm -rf /\",\"purpose\":\"oops\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	result := lastToolResult(t, env.coder)
	if !strings.Contains(result.Text(), "chose not to run") {
		t.Errorf("declined result = %q, want a not-run message", result.Text())
	}
}

// TestToolEditReflects is the reflection-as-tool-error path: a first call
// whose search doesn't match produces a tool-error result and the turn
// re-sends on that result — no synthetic user turn — and the second, correct
// call succeeds.
func TestToolEditReflects(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-reflect","source":"authored"}
{"kind":"fs","path":"a.txt","content":"hello\nworld\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"change world to mars"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"planet\\n\",\"new_string\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_2","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"world\\n\",\"new_string\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":1}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	// The reflection must NOT inject a synthetic user turn: the only user
	// message is the original request. The sequence is
	// user, assistant(call_1), tool(error), assistant(call_2), tool(ok),
	// assistant(closing).
	history := env.coder.doneMessages
	roles := make([]string, len(history))
	users := 0
	for i, m := range history {
		roles[i] = m.Role
		if m.Role == llm.RoleUser {
			users++
		}
	}
	if users != 1 {
		t.Errorf("user messages = %d, want 1 (no synthetic reflection turn); roles=%v", users, roles)
	}
	want := []string{"user", "assistant", "tool", "assistant", "tool", "assistant"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("history roles = %v, want %v", roles, want)
	}
	// The first tool result must carry the search-failure error, not a success
	// message.
	if !strings.Contains(history[2].Text(), "was not found") {
		t.Errorf("first tool result = %q, want a search-failure message", history[2].Text())
	}
}

// TestToolLoopSpansFiles is the regression case the closed loop exists for: a
// change that touches code and its separately-stored test. Before the loop
// closed, the turn ended the moment the model emitted its first batch of calls,
// so the model never saw the test output and never got to the second file —
// which is why the failure only showed when a change spanned two files.
func TestToolLoopSpansFiles(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-loop-two-files","source":"authored"}
{"kind":"fs","path":"app.go","content":"package app\n\nfunc Greet() string { return \"hello\" }\n"}
{"kind":"fs","path":"app_test.go","content":"package app\n\nfunc TestGreet(t *testing.T) { _ = \"hello\" }\n"}
{"kind":"chat","editable":["app.go","app_test.go"]}
{"kind":"user","text":"change the greeting to hey"}
{"kind":"confirm","prompt":"Run shell command?","answer":"yes"}
{"kind":"command","block":"go test ./...","exit":1,"output":"FAIL: want hello, got hey\n"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"edit","tool_args":"{\"path\":\"app.go\",\"old_string\":\"func Greet() string { return \\\"hello\\\" }\\n\",\"new_string\":\"func Greet() string { return \\\"hey\\\" }\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_2","tool_name":"bash","tool_args":"{\"command\":\"go test ./...\",\"purpose\":\"check the change\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_3","tool_name":"edit","tool_args":"{\"path\":\"app_test.go\",\"old_string\":\"func TestGreet(t *testing.T) { _ = \\\"hello\\\" }\\n\",\"new_string\":\"func TestGreet(t *testing.T) { _ = \\\"hey\\\" }\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"app.go","content":"package app\n\nfunc Greet() string { return \"hey\" }\n"}
{"kind":"expect_fs","path":"app_test.go","content":"package app\n\nfunc TestGreet(t *testing.T) { _ = \"hey\" }\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	// Three tool rounds, then the closing answer. The rounds are work steps,
	// not error reflections — nothing went wrong.
	if env.coder.numSteps != 3 {
		t.Errorf("steps = %d, want 3", env.coder.numSteps)
	}
	if env.coder.numReflections != 0 {
		t.Errorf("reflections = %d, want 0 (no mistakes were made)", env.coder.numReflections)
	}
	if env.stub.Remaining() != 0 {
		t.Errorf("%d scripted turns unconsumed: the loop stopped early", env.stub.Remaining())
	}

	// The failing test output must have reached the model as a tool result —
	// that is the observation the whole loop exists to deliver.
	var sawOutput bool
	for _, m := range append(append([]llm.Message(nil), env.coder.doneMessages...), env.coder.curMessages...) {
		if m.Role == llm.RoleTool && strings.Contains(m.Text(), "want hello, got hey") {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Error("the command output never reached the model as a tool result")
	}
}

// TestToolLoopBudgetStops confirms the step budget is a checkpoint the user
// clears: at maxSteps the turn reports what it has done and stops when the
// user declines, leaving the work so far applied.
func TestToolLoopBudgetStops(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"kind":"meta","v":1,"scenario":"tool-loop-budget","source":"authored"}
{"kind":"fs","path":"a.txt","content":"start\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"keep rewriting a.txt"}
{"kind":"confirm","prompt":"Keep going?","answer":"no"}
`)
	// One more stream than the budget would let run, so an off-by-one that
	// overshot the checkpoint would consume it and fail the Remaining check.
	for i := range maxSteps + 1 {
		fmt.Fprintf(&b, `{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_%d","tool_name":"write","tool_args":"{\"path\":\"a.txt\",\"content\":\"pass %d\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}`+"\n", i, i)
	}
	sc := inlineScenario(t, b.String())

	out := &captureOut{}
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.Out = out
	})
	env.coder.Run(t.Context(), sc.User)

	if env.coder.numSteps != maxSteps {
		t.Errorf("steps = %d, want %d", env.coder.numSteps, maxSteps)
	}
	if env.stub.Remaining() != 1 {
		t.Errorf("remaining turns = %d, want 1 (the loop must stop at the budget)", env.stub.Remaining())
	}
	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "has run 25 steps and edited 1 file") {
		t.Errorf("missing the budget summary:\n%s", joined)
	}
	// Declining stops the turn but keeps the work: the last applied edit stands.
	data, err := os.ReadFile(filepath.Join(env.dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != fmt.Sprintf("pass %d\n", maxSteps-1) {
		t.Errorf("a.txt = %q, want the last applied edit to stand", got)
	}
}

// captureOut records the user-facing lines so a test can assert on them; the
// streaming Output methods are no-ops.
type captureOut struct{ lines []string }

func (o *captureOut) Printf(format string, args ...any) {
	o.lines = append(o.lines, fmt.Sprintf(format, args...))
}
func (o *captureOut) Toolf(format string, args ...any)    { o.Printf(format, args...) }
func (o *captureOut) Warningf(format string, args ...any) { o.Printf(format, args...) }
func (o *captureOut) Errorf(format string, args ...any)   { o.Printf(format, args...) }
func (o *captureOut) StreamText(string)                   {}
func (o *captureOut) StreamReasoning(string)              {}
func (o *captureOut) StreamToolCall(int, string, string)  {}
func (o *captureOut) FlushStream()                        {}

// echoRunner returns fixed output for any command.
type echoRunner struct {
	exit   int
	output string
}

func (r echoRunner) Run(context.Context, string, string) (int, string, error) {
	return r.exit, r.output, nil
}

// TestRunAndShowDisplaysOutput confirms a run command's captured output is
// echoed to the user (bug #3), not only returned to the model as the result.
func TestRunAndShowDisplaysOutput(t *testing.T) {
	out := &captureOut{}
	c := &Coder{Out: out, Runner: echoRunner{exit: 0, output: "hello\nworld\n"}}
	exit, output := c.runAndShow(context.Background(), "echo stuff")

	if exit != 0 || output != "hello\nworld\n" {
		t.Fatalf("runAndShow = (%d, %q), want (0, %q)", exit, output, "hello\nworld\n")
	}
	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "Running echo stuff") {
		t.Errorf("missing the Running line:\n%s", joined)
	}
	if !strings.Contains(joined, "hello\nworld") {
		t.Errorf("command output not shown to the user:\n%s", joined)
	}
}

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
	// The read-only tools always come first, ask_user_question with them (it
	// mutates nothing), then code (it computes, and mutates nothing either);
	// bash is gated off here, and no check is configured.
	want := []string{"read", "grep", "glob", "ls", "ask_user_question", "code", "edit", "write", "commit"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v (bash gated off)", names, want)
	}
}

// TestToolSetGating covers the conditional parts of the tool set: bash appears
// only when shell commands are enabled, check only when the project configured
// checks, and ask mode offers nothing that changes anything — which is why the
// ask-mode row is the one that pins commit's absence.
func TestToolSetGating(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Coder)
		want   []string
	}{
		{
			"shell enabled, no check configured",
			func(c *Coder) { c.editFormat = "tool"; c.SuggestShellCommands = true },
			[]string{"read", "grep", "glob", "ls", "ask_user_question", "code", "edit", "write", "bash", "commit"},
		},
		{
			"check configured",
			func(c *Coder) {
				c.editFormat = "tool"
				c.SuggestShellCommands = true
				c.Check = []config.Check{{Name: "test", Argv: []string{"go", "test", "./..."}}}
			},
			[]string{"read", "grep", "glob", "ls", "ask_user_question", "code", "edit", "write", "bash", "check", "commit"},
		},
		{
			"ask mode is read-only",
			func(c *Coder) { c.editFormat = "ask"; c.SuggestShellCommands = true },
			[]string{"read", "grep", "glob", "ls", "ask_user_question", "code"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Coder{OfferCode: true}
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

// TestCheckToolListsChecksInDescription confirms the schema tells the model
// which names exist and what each one runs — the tool takes a name and never a
// command, so the description is the only place that information can live.
func TestCheckToolListsChecksInDescription(t *testing.T) {
	def := checkTool([]config.Check{
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
// auto-commit path (and its message) can be asserted. When asked is non-nil,
// Commit appends the fnames it received, so a test can assert exactly what
// git was asked to record. root, when set, is what Root answers — the
// commit-path filter needs a real root to test containment against.
type committingRepo struct {
	tracked []string
	asked   [][]string
	root    string
}

func (r *committingRepo) Root() string             { return r.root }
func (r *committingRepo) TrackedFiles() []string   { return r.tracked }
func (r *committingRepo) PathInRepo(_ string) bool { return true }
func (r *committingRepo) IsDirty(_ string) bool    { return false }
func (r *committingRepo) GitIgnored(_ string) bool { return false }
func (r *committingRepo) HeadSHA() string          { return "deadbeef" }
func (r *committingRepo) Commit(fnames []string, _, _ string, _ bool) (string, string, bool, error) {
	if r.asked != nil {
		r.asked = append(r.asked, fnames)
	}
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
{"kind":"expect_history","messages":[{"role":"user","text":"change world to mars"},{"role":"assistant","text":""},{"role":"tool","text":"Applied the edit to a.txt."},{"role":"assistant","text":"Done."}]}
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

// countingRepo records what it was asked to commit, so a test can pin how many
// commits one turn produces and over which files.
type countingRepo struct {
	committingRepo

	dirty bool // report every file as dirty, as a tree with the user's own work is
	calls [][]string
	attrs []bool
	msgs  []string // what the caller asked for, "" when it wanted one generated
}

func (r *countingRepo) IsDirty(string) bool { return r.dirty }

func (r *countingRepo) Commit(fnames []string, context, message string, attributed bool) (string, string, bool, error) {
	r.calls = append(r.calls, fnames)
	r.attrs = append(r.attrs, attributed)
	r.msgs = append(r.msgs, message)
	return r.committingRepo.Commit(fnames, context, message, attributed)
}

// TestOneCommitPerTurn is the point of moving the commit to turn end. A turn
// that edits two files across two sends is one change, and the history should
// say so once — not twice, with each half described by a model that saw only
// its own fragment.
func TestOneCommitPerTurn(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"one-commit-per-turn","source":"authored"}
{"kind":"fs","path":"a.txt","content":"hello\nworld\n"}
{"kind":"fs","path":"b.txt","content":"keep\nthis\n"}
{"kind":"chat","editable":["a.txt","b.txt"]}
{"kind":"user","text":"change world to mars, then this to that"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"world\\n\",\"new_string\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_2","tool_name":"edit","tool_args":"{\"path\":\"b.txt\",\"old_string\":\"this\\n\",\"new_string\":\"that\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_fs","path":"b.txt","content":"keep\nthat\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	repo := &countingRepo{committingRepo: committingRepo{tracked: []string{"a.txt", "b.txt"}}}
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.AutoCommits = true
		c.Repo = repo
	})
	env.run(t)

	if len(repo.calls) != 1 {
		t.Fatalf("Commit called %d times, want 1: %v", len(repo.calls), repo.calls)
	}
	if got := strings.Join(repo.calls[0], ","); got != "a.txt,b.txt" {
		t.Errorf("committed %q, want both files the turn touched", got)
	}
}

// TestDirtyCommitIgnoresTheTurnsOwnEdits is a regression found live. Once the
// commit moved to turn end, a turn's first edit leaves the file dirty, so its
// second edit to the same file looked like the user's uncommitted work. The
// pre-edit dirty commit then swept the turn's changes into an unattributed
// commit — one /undo and /squash both refuse, because Strument has no record of
// having made it.
func TestDirtyCommitIgnoresTheTurnsOwnEdits(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"dirty-commit-once","source":"authored"}
{"kind":"fs","path":"a.txt","content":"one\ntwo\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"change both lines, one at a time"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"one\\n\",\"new_string\":\"ONE\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_2","tool_name":"edit","tool_args":"{\"path\":\"a.txt\",\"old_string\":\"two\\n\",\"new_string\":\"TWO\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"ONE\nTWO\n"}
`+closingTurn)
	repo := &countingRepo{committingRepo: committingRepo{tracked: []string{"a.txt"}}, dirty: true}
	out := &captureOut{}
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.AutoCommits = true
		c.Repo = repo
		c.Out = out
	})
	env.run(t)

	// Exactly two: the user's pre-existing work before the first edit, then the
	// turn's own at the end. A third would be the turn committing itself away.
	if len(repo.calls) != 2 {
		t.Fatalf("Commit called %d times, want 2 (dirty, then the turn): %v", len(repo.calls), repo.calls)
	}
	if repo.attrs[0] || !repo.attrs[1] {
		t.Errorf("attribution = %v, want the dirty commit unattributed and the turn's attributed", repo.attrs)
	}
	if got := strings.Count(strings.Join(out.lines, "\n"), "Committing a.txt before applying edits."); got != 1 {
		t.Errorf("dirty-commit notice count = %d, want 1; output:\n%s", got, strings.Join(out.lines, "\n"))
	}
}

// TestToolEditCommits asserts the turn's single commit happens and is reported
// to the user. The commit is deliberately *not* in the tool result: it lands at
// turn end, after the call has already been answered, and its hash is a fact
// about the user's history rather than about whether the edit applied. What the
// model gets is what it needs — the edit landed, in this file.
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
	out := &captureOut{}
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.AutoCommits = true
		c.Repo = &committingRepo{tracked: []string{"a.txt"}}
		c.Out = out
	})
	env.run(t)

	if joined := strings.Join(out.lines, "\n"); !strings.Contains(joined, "Commit abc1234") {
		t.Errorf("the commit was not reported to the user:\n%s", joined)
	}
	result := lastToolResult(t, env.coder).Text()
	if !strings.Contains(result, "a.txt") {
		t.Errorf("tool result = %q, want it to name the file it edited", result)
	}
	if strings.Contains(result, "abc1234") {
		t.Errorf("tool result = %q: the commit does not belong in it", result)
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

// recordingConfirmer captures the request it was asked, so a test can assert
// on what the user would have been shown rather than only on the answer.
type recordingConfirmer struct {
	got    []ConfirmRequest
	answer bool
}

func (rc *recordingConfirmer) Confirm(req ConfirmRequest) ConfirmResult {
	rc.got = append(rc.got, req)
	return ConfirmResult{Yes: rc.answer}
}

// TestBashRequiresAPurpose pins the schema. The prompt is worth exactly what
// the user reads off it, and a model that is not asked for a claim about the
// command writes a label or nothing at all.
func TestBashRequiresAPurpose(t *testing.T) {
	required, ok := bashTool().Parameters["required"].([]any)
	if !ok {
		t.Fatalf("bash schema has no required list: %#v", bashTool().Parameters)
	}
	if !slices.Contains(required, any("command")) || !slices.Contains(required, any("purpose")) {
		t.Errorf("required = %v, want both command and purpose", required)
	}
}

// askToolArgs is a one-question argument object with two options.
const askToolArgs = `{"questions":[{"question":"Which format?","options":[` +
	`{"label":"RFC 3339","description":"matches the other loggers"},` +
	`{"label":"Unix epoch","description":"compact"}]}]}`

// askToolArgsJSONL is the same arguments JSON-escaped for embedding in a
// fixture stream row's tool_args.
const askToolArgsJSONL = `{\"questions\":[{\"question\":\"Which format?\",\"options\":[` +
	`{\"label\":\"RFC 3339\",\"description\":\"matches the other loggers\"},` +
	`{\"label\":\"Unix epoch\",\"description\":\"compact\"}]}]}`

// askStreamTurn is a stream row whose one tool call asks the question above.
const askStreamTurn = `{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1",` +
	`"tool_name":"ask_user_question","tool_args":"` + askToolArgsJSONL + `"},` +
	`{"kind":"Finish","finish_reason":"tool_calls"}]}`

// TestParseAskAnswer pins the whole-fallback rule from the spec: the typed
// line resolves to labels only when every comma-separated token is a valid
// in-range index; anything else is the entire raw input as one free-text
// answer, never a partial interpretation.
func TestParseAskAnswer(t *testing.T) {
	req := AskRequest{Question: "q", Options: []AskOption{
		{Label: "one"}, {Label: "two"}, {Label: "three"},
	}}
	for _, tc := range []struct {
		input string
		multi bool
		want  []string
	}{
		{"1", false, []string{"one"}},
		{" 2 ", false, []string{"two"}},
		{"1, 3", true, []string{"one", "three"}},
		// The "Other" row (index 4) is a blank custom answer.
		{"4", false, nil},
		// Out of range and unparseable tokens fall through to raw text.
		{"0", false, []string{"0"}},
		{"5", false, []string{"5"}},
		{"1.5", false, []string{"1.5"}},
		{"rfc3339, but with milliseconds", false, []string{"rfc3339, but with milliseconds"}},
		// A partial parse never partially selects.
		{"1, x", true, []string{"1, x"}},
		// Multi-index to a single-select question is out of range for it.
		{"1,2", false, []string{"1,2"}},
		{"", false, nil},
	} {
		req.MultiSelect = tc.multi
		got := ParseAskAnswer(req, tc.input)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("ParseAskAnswer(%q, multi=%v) = %v, want %v", tc.input, tc.multi, got, tc.want)
		}
	}
}

// TestParseAskArgs covers the decode-and-validate half: the caps the schema
// states are enforced on the Go side too, and a violation is a model-facing
// message.
func TestParseAskArgs(t *testing.T) {
	q := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"questions":[`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"question":"q%d","options":[{"label":"a","description":""},{"label":"b","description":""}]}`, i)
		}
		b.WriteString(`]}`)
		return b.String()
	}
	for _, tc := range []struct {
		name, args, wantErr string
	}{
		{"valid", askToolArgs, ""},
		{"bad json", `{`, "not valid JSON"},
		{"no questions", `{"questions":[]}`, "1 to 5 questions"},
		{"too many questions", q(6), "1 to 5 questions"},
		{"one question", q(1), ""},
		{"five questions", q(5), ""},
		{"one option", `{"questions":[{"question":"q","options":[{"label":"a","description":""}]}]}`, "2 to 4 options"},
		{"missing question text", `{"questions":[{"options":[{"label":"a","description":""},{"label":"b","description":""}]}]}`, "no \"question\" text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, msg := parseAskArgs(call(toolAskUser, tc.args))
			if tc.wantErr == "" {
				if msg != "" {
					t.Fatalf("parse error = %q, want none", msg)
				}
				return
			}
			if !strings.Contains(msg, tc.wantErr) {
				t.Errorf("parse error = %q, want it to contain %q", msg, tc.wantErr)
			}
		})
	}
}

// TestAskUserReplay drives a full ask_user_question turn through the fixture
// harness: the scripted "ask" row answers the question by index, the result
// text carries question and label, the narration line prints, and the turn is
// a plain work step — no reflection was spent.
func TestAskUserReplay(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"ask-user","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"add a timestamp to the log line"}
{"kind":"ask","question":"Which format?","answer":"1"}
`+askStreamTurn+`
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	out := &captureOut{}
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.Out = out
	})
	env.run(t)

	result := lastToolResult(t, env.coder)
	if !strings.Contains(result.Text(), "The user answered:") ||
		!strings.Contains(result.Text(), `"Which format?" → "RFC 3339"`) {
		t.Errorf("result = %q, want the question and the chosen label", result.Text())
	}
	// One narration line: the interactive Asker already printed the question
	// and the option list above the answer prompt, so the screen and the
	// transcript record get the answer — with the question attached, so the
	// line is self-describing — rather than a replay of the question block.
	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, `User answered "RFC 3339"`) {
		t.Errorf("missing the answer narration line:\n%s", joined)
	}
	if strings.Contains(joined, "‹question›") || strings.Contains(joined, "‹options›") {
		t.Errorf("the narration replays the question block it follows:\n%s", joined)
	}
	// An ask is an ordinary work step, so it must have spent the step budget.
	if env.coder.numSteps != 1 {
		t.Errorf("steps = %d, want 1", env.coder.numSteps)
	}
}

// TestAskUserFreeTextAnswer pins the escape hatch end to end: a scripted
// answer that is not an index is the whole raw string, exactly as if the user
// had typed it.
func TestAskUserFreeTextAnswer(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"ask-user-free-text","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"add a timestamp to the log line"}
{"kind":"ask","question":"Which format?","answer":"rfc3339, but with milliseconds"}
`+askStreamTurn+`
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	if result := lastToolResult(t, env.coder).Text(); !strings.Contains(result, `"rfc3339, but with milliseconds"`) {
		t.Errorf("result = %q, want the raw free-text answer", result)
	}
}

// TestAskUserNoTerminal is script mode: a nil Asker must not hang and must
// not silently pick option 1 — the model is told to proceed on its best
// judgment, and that answer is not the model's fault, so no reflection is
// spent.
func TestAskUserNoTerminal(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"ask-user-no-terminal","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"add a timestamp"}
`+askStreamTurn+`
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`+closingTurn)
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.Asker = nil
	})
	env.run(t)

	result := lastToolResult(t, env.coder).Text()
	if !strings.Contains(result, "unavailable without an interactive terminal") {
		t.Errorf("result = %q, want the no-terminal message", result)
	}
	if strings.Contains(result, "RFC 3339") {
		t.Errorf("result = %q: a nil Asker must never auto-select an option", result)
	}
	if env.coder.numReflections != 0 {
		t.Errorf("reflections = %d, want 0 (not the model's fault)", env.coder.numReflections)
	}
}

// TestAskUserValidationReflects pins that a malformed call reflects like any
// other bad tool argument: the failure is the call's tool result and the turn
// re-sends on it.
func TestAskUserValidationReflects(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"ask-user-bad-args","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"ask me something"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"ask_user_question","tool_args":"{\"questions\":[]}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_2","tool_name":"ask_user_question","tool_args":"`+askToolArgsJSONL+`"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"ask","question":"Which format?","answer":"2"}
{"kind":"expect_outcome","outcome":"Success","reflections":1}
`+closingTurn)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	hist := history(env.coder)
	// user, assistant(call_1), tool(error), assistant(call_2), tool(answer),
	// assistant(closing).
	if len(hist) != 6 {
		t.Fatalf("history = %s", dumpHistory(hist))
	}
	if !strings.Contains(hist[2].Text(), "1 to 5 questions") {
		t.Errorf("first tool result = %q, want the validation failure", hist[2].Text())
	}
	if !strings.Contains(hist[4].Text(), `"Unix epoch"`) {
		t.Errorf("second tool result = %q, want the answered label", hist[4].Text())
	}
}

// TestBashConfirmationCarriesThePurpose is the point of requiring one: the
// model's claim reaches the confirmation, separately from the command, so a
// renderer can draw them differently.
func TestBashConfirmationCarriesThePurpose(t *testing.T) {
	for _, tc := range []struct {
		name, args, wantPurpose string
	}{
		{"stated", `{"command":"go test ./...","purpose":"re-run the suite after the parser fix"}`,
			"re-run the suite after the parser fix"},
		// Required in the schema, but a model that omits it is not sent back for
		// one: the absence is shown to the user instead.
		{"omitted", `{"command":"go test ./..."}`, ""},
		{"blank", `{"command":"go test ./...","purpose":"   "}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := &recordingConfirmer{answer: false}
			c := &Coder{Out: &captureOut{}, Confirm: rc, SuggestShellCommands: true}
			cmd, msg := parseCommandArgs(call("bash", tc.args))
			if msg != "" {
				t.Fatalf("parse: %s", msg)
			}
			c.runShellTool(context.Background(), cmd)

			if len(rc.got) != 1 {
				t.Fatalf("confirmed %d times, want 1", len(rc.got))
			}
			req := rc.got[0]
			if req.Command != "go test ./..." {
				t.Errorf("Command = %q, want the command", req.Command)
			}
			if req.Purpose != tc.wantPurpose {
				t.Errorf("Purpose = %q, want %q", req.Purpose, tc.wantPurpose)
			}
		})
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
// clears: at MaxSteps the turn reports what it has done and stops when the
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
	for i := range 25 + 1 {
		fmt.Fprintf(&b, `{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_%d","tool_name":"write","tool_args":"{\"path\":\"a.txt\",\"content\":\"pass %d\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}`+"\n", i, i)
	}
	sc := inlineScenario(t, b.String())

	out := &captureOut{}
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.Out = out
	})
	env.coder.Run(t.Context(), sc.User)

	if env.coder.numSteps != env.coder.MaxSteps {
		t.Errorf("steps = %d, want %d", env.coder.numSteps, env.coder.MaxSteps)
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
	if got := string(data); got != fmt.Sprintf("pass %d\n", env.coder.MaxSteps-1) {
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
func (o *captureOut) Link(target string)                  { o.Printf("%s", target) }
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
	if !strings.Contains(joined, `Running "echo stuff"`) {
		t.Errorf("missing the Running line:\n%s", joined)
	}
	if !strings.Contains(joined, "hello\nworld") {
		t.Errorf("command output not shown to the user:\n%s", joined)
	}
}

// An old_string that appears more than once fails rather than editing the
// first of them.
//
// DoReplace takes the first occurrence and returns success, so this used to
// report "Applied the edit to x" after changing whichever span came first —
// the model reasoning from a false success, which is the exact criticism
// edit-tool-bench levels at fuzzy editing. Exact matching does not prevent it;
// exact is not unique. Found by building a fixture meant to produce an
// ambiguity failure and watching it succeed.
func TestAmbiguousEditFailsRatherThanTakingTheFirstSite(t *testing.T) {
	for _, tc := range []struct{ name, body, search string }{
		{"same text on two lines", "twice\nand twice\n", "twice"},
		{"two identical lines", "dup\ndup\n", "dup"},
		{"two identical blocks", "a\nb\nx\na\nb\n", "a\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			c := toolCoder(t, dir)
			results := map[string]string{}
			var failed bool
			c.applyToolEdits([]plannedEdit{{
				callID: "1", path: "f.txt", search: tc.search, replace: "REPLACED",
			}}, results, &failed)

			if !failed {
				t.Error("an ambiguous edit was not reported as a match failure")
			}
			if !strings.Contains(results["1"], "ambiguous") {
				t.Errorf("result does not say why: %q", results["1"])
			}
			after, err := os.ReadFile(filepath.Join(dir, "f.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.body {
				t.Errorf("the file was changed anyway:\n%q", after)
			}
		})
	}
}

// The counter-half: a search that is genuinely unique still applies, including
// one whose text appears once as a whole block but whose first line repeats.
func TestUniqueEditStillApplies(t *testing.T) {
	dir := t.TempDir()
	body := "a\nb\nx\na\nc\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	results := map[string]string{}
	var failed bool
	c.applyToolEdits([]plannedEdit{{callID: "1", path: "f.txt", search: "a\nb", replace: "Q"}}, results, &failed)

	if failed {
		t.Errorf("a unique edit was refused: %q", results["1"])
	}
	after, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(after) != "Q\nx\na\nc\n" {
		t.Errorf("got %q", after)
	}
}

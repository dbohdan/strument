package coder

import (
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// toolMode sets the coder to the "tool" edit format for a replay.
func toolMode(c *Coder) { c.editFormat = "tool" }

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

// TestToolEditApplies replays a single replace_in_file tool call and asserts
// the edit lands on disk, the outcome is a clean success, and the history is
// well-formed: user, assistant-with-tool-call, tool-result.
func TestToolEditApplies(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-edit","source":"authored"}
{"kind":"fs","path":"a.txt","content":"hello\nworld\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"change world to mars"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"replace_in_file","tool_args":"{\"path\":\"a.txt\",\"search\":\"world\\n\",\"replace\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_history","messages":[{"role":"user","text":"change world to mars"},{"role":"assistant","text":""},{"role":"tool","text":"I applied your changes to the files."}]}
`)
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

// TestToolCreateFile replays a create_file call and asserts the new file is
// written with its contents.
func TestToolCreateFile(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-create","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"add a greeting file"}
{"kind":"confirm","prompt":"Create new file?","answer":"yes"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"create_file","tool_args":"{\"path\":\"hi.txt\",\"content\":\"hi there\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"hi.txt","content":"hi there\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, toolMode)
	env.run(t)
}

// TestToolEditCommits asserts that with git auto-commit on, the applied
// edit's tool result names the commit hash — the direct-apply safety net.
func TestToolEditCommits(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-commit","source":"authored"}
{"kind":"fs","path":"a.txt","content":"hello\nworld\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"change world to mars"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"replace_in_file","tool_args":"{\"path\":\"a.txt\",\"search\":\"world\\n\",\"replace\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.AutoCommits = true
		c.Repo = &committingRepo{tracked: []string{"a.txt"}}
	})
	env.run(t)

	result := env.coder.doneMessages[2].Text()
	if !strings.Contains(result, "abc1234") {
		t.Errorf("tool result = %q, want it to name commit abc1234", result)
	}
}

// TestToolSuggestCommand replays a suggest_command call: the user confirms,
// the command runs, and its output comes back as the tool result.
func TestToolSuggestCommand(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-command","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"run the tests"}
{"kind":"confirm","prompt":"Run shell command?","answer":"yes"}
{"kind":"command","block":"go test ./...","exit":0,"output":"ok\n"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"suggest_command","tool_args":"{\"command\":\"go test ./...\",\"purpose\":\"run the tests\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	result := env.coder.curMessages[len(env.coder.curMessages)-1]
	if result.Role != llm.RoleTool || result.ToolCallID != "call_1" {
		t.Fatalf("last message = %s/%s, want tool/call_1", result.Role, result.ToolCallID)
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
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"suggest_command","tool_args":"{\"command\":\"rm -rf /\",\"purpose\":\"oops\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	result := env.coder.curMessages[len(env.coder.curMessages)-1]
	if !strings.Contains(result.Text(), "chose not to run") {
		t.Errorf("declined result = %q, want a not-run message", result.Text())
	}
}

// TestToolRequestFiles replays a request_files call: the user confirms and the
// file joins the chat.
func TestToolRequestFiles(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-request","source":"authored"}
{"kind":"fs","path":"lib.go","content":"package lib\n"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"edit the library"}
{"kind":"confirm","prompt":"Add file to the chat?","answer":"yes"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"request_files","tool_args":"{\"paths\":[\"lib.go\"],\"reason\":\"need to edit it\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	if !slices.Contains(env.coder.inchatRelativeFiles(), "lib.go") {
		t.Errorf("lib.go not added to chat; chat = %v", env.coder.inchatRelativeFiles())
	}
	result := env.coder.curMessages[len(env.coder.curMessages)-1]
	if !strings.Contains(result.Text(), "Added to the chat: lib.go") {
		t.Errorf("request result = %q, want an added-files summary", result.Text())
	}
}

// TestToolRequestMissingFile confirms a request for a file that doesn't exist
// reports it without prompting.
func TestToolRequestMissingFile(t *testing.T) {
	sc := inlineScenario(t, `
{"kind":"meta","v":1,"scenario":"tool-request-missing","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"edit ghost.go"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"request_files","tool_args":"{\"paths\":[\"ghost.go\"],\"reason\":\"need it\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	result := env.coder.curMessages[len(env.coder.curMessages)-1]
	if !strings.Contains(result.Text(), "not found") {
		t.Errorf("missing-file result = %q, want a not-found note", result.Text())
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
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_1","tool_name":"replace_in_file","tool_args":"{\"path\":\"a.txt\",\"search\":\"planet\\n\",\"replace\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"call_2","tool_name":"replace_in_file","tool_args":"{\"path\":\"a.txt\",\"search\":\"world\\n\",\"replace\":\"mars\\n\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\nmars\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":1}
`)
	env := setupScenario(t, sc, toolMode)
	env.run(t)

	// The reflection must NOT inject a synthetic user turn: the only user
	// message is the original request. The sequence is
	// user, assistant(call_1), tool(error), assistant(call_2), tool(ok).
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
	want := []string{"user", "assistant", "tool", "assistant", "tool"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("history roles = %v, want %v", roles, want)
	}
	// The first tool result must carry the search-failure error, not a success
	// message.
	if !strings.Contains(history[2].Text(), "was not found") {
		t.Errorf("first tool result = %q, want a search-failure message", history[2].Text())
	}
}

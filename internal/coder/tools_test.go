package coder

import (
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

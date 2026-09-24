package coder

import (
	"slices"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func assistantSays(text string, calls ...llm.ToolCall) llm.Message {
	m := llm.TextMessage(llm.RoleAssistant, text)
	m.ToolCalls = calls
	return m
}

func TestCompletionWordsTakesOneTokenSpansAndLookups(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "why is `max_output` ignored when `--no-git` is set?"),
		assistantSays("Looking.",
			llm.ToolCall{ID: "1", Name: toolSymbol, Arguments: `{"name":"formatWindow"}`},
			llm.ToolCall{ID: "2", Name: toolGrep, Arguments: `{"pattern":"PrefillSupported"}`},
			llm.ToolCall{ID: "3", Name: toolGrep, Arguments: `{"pattern":"func .*Run\\("}`},
		),
		llm.ToolResult("1", "internal/render/window.go:12"),
		llm.HarnessNote("a note with `harnessWord` in it"),
		assistantSays("It is `applyConfig()` in `internal/coder/applyconfig.go`; run `go test ./...` " +
			"to check.\n\n```go\nfunc fencedName() {}\n```\nAlso `x`."),
	}
	got := c.CompletionWords(5)
	for _, want := range []string{"max_output", "--no-git", "formatWindow", "PrefillSupported",
		"applyConfig", "internal/coder/applyconfig.go"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q from %q", want, got)
		}
	}
	for _, not := range []string{"go test ./...", "fencedName", "x", "harnessWord", "func .*Run\\("} {
		if slices.Contains(got, not) {
			t.Errorf("%q should not be a candidate: %q", not, got)
		}
	}
	// Most recent first: the last answer's words come before the question's.
	if slices.Index(got, "applyConfig") > slices.Index(got, "max_output") {
		t.Errorf("not most recent first: %q", got)
	}
}

// Recency is the relevance filter: a name nobody has mentioned for the last
// turns is no longer offered. A harness note is not a turn.
func TestCompletionWordsTurnOver(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "look at `oldName`"),
		assistantSays("Done with `oldName`."),
	}
	c.curMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "now `newName`"),
		llm.HarnessNote("not a turn"),
		assistantSays("Here is `newName`."),
	}
	if got := c.CompletionWords(1); slices.Contains(got, "oldName") || !slices.Contains(got, "newName") {
		t.Errorf("one turn back = %q, want newName only", got)
	}
	if got := c.CompletionWords(2); !slices.Contains(got, "oldName") {
		t.Errorf("two turns back = %q, want oldName too", got)
	}
}

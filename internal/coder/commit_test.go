package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// TestCommitContextIncludesToolCalls pins the half that used to be invisible.
// Message.Text() returns Content, and a model's calls live in ToolCalls, so a
// context built from text alone handed the commit-message model the tool
// results without the calls that produced them — every purpose string, path
// and query dropped.
func TestCommitContextIncludesToolCalls(t *testing.T) {
	c := &Coder{curMessages: []llm.Message{
		llm.TextMessage("user", "Make the poll interval configurable."),
		{
			Role:    "assistant",
			Content: llm.TextContent("Checking the current value first."),
			ToolCalls: []llm.ToolCall{
				{Name: "read", Arguments: `{"path": "poll/poll.go"}`},
				{Name: "bash", Arguments: `{"purpose": "confirm the tests still pass", "command": "go test ./..."}`},
			},
		},
		llm.ToolResult("1", "poll/poll.go (5 lines)\n1\tpackage poll\n"),
	}}

	got := c.commitContext()
	for _, want := range []string{
		"Make the poll interval configurable.",
		"CALL: read",
		"poll/poll.go",
		"CALL: bash",
		"confirm the tests still pass",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("commit context is missing %q:\n%s", want, got)
		}
	}
}

// TestCommitContextCapsToolArguments keeps an edit call from carrying the whole
// new text of a file into a request whose job is to write one subject line —
// especially since the diff is passed to the commit model separately.
func TestCommitContextCapsToolArguments(t *testing.T) {
	c := &Coder{curMessages: []llm.Message{{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{{
			Name:      "edit",
			Arguments: `{"path": "poll/poll.go", "new_text": "` + strings.Repeat("x", 50_000) + `"}`,
		}},
	}}}

	got := c.commitContext()
	if len(got) > maxCommitArgs+200 {
		t.Errorf("commit context is %d bytes; the cap is %d", len(got), maxCommitArgs)
	}
	// What the cap keeps has to be the identifying head, not an arbitrary slice.
	if !strings.Contains(got, "poll/poll.go") {
		t.Errorf("truncation dropped the path, which is the part worth keeping:\n%s", got)
	}
}

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// countingRunner records that a command reached it.
type countingRunner struct{ runs int }

func (r *countingRunner) Run(context.Context, string, string) (int, string, error) {
	r.runs++
	return 0, "ran", nil
}

// Ask mode offered read-only tools and enforced that with nothing else: the
// dispatcher ran whatever a model named. Found from a session log where MiMo,
// asked to try bash although it was not in the list, did — and edit, write and
// commit were one call away the same way.
func TestAskModeRunsOnlyWhatItOffers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	runner := &countingRunner{}
	c.Runner = runner
	c.SetEditFormat("ask")
	c.partialToolCalls = []llm.ToolCall{
		{ID: "b", Name: toolBash, Arguments: `{"command":"echo hi"}`},
		{ID: "e", Name: toolEdit, Arguments: `{"path":"a.txt","old_string":"hello","new_string":"bye"}`},
		{ID: "r", Name: toolRead, Arguments: `{"path":"a.txt"}`},
	}
	c.curMessages = append(c.curMessages, llm.Message{Role: llm.RoleAssistant, ToolCalls: c.partialToolCalls})
	c.applyToolCalls(context.Background())

	if runner.runs != 0 {
		t.Error("bash ran in ask mode")
	}
	if got, _ := os.ReadFile(path); string(got) != "hello\n" {
		t.Errorf("edit changed the file in ask mode: %q", got)
	}
	results := map[string]string{}
	for _, m := range c.curMessages {
		if m.Role == llm.RoleTool {
			results[m.ToolCallID] = m.Text()
		}
	}
	for _, id := range []string{"b", "e"} {
		if !strings.Contains(results[id], "ask mode") || !strings.Contains(results[id], "/code") {
			t.Errorf("result for %s = %q, want it to say ask mode withholds it and how to switch", id, results[id])
		}
	}
	if !strings.Contains(results["r"], "hello") {
		t.Errorf("an offered tool stopped working: %q", results["r"])
	}
}

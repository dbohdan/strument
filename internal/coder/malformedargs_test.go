package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// A tool call cut off mid-argument is not a model that cannot write JSON, and
// saying so sends it back to try the same oversized call again. Ling 3.0 Flash
// spent its 32,768-token budget on reasoning, began a write, and was cut in the
// middle of the file's <style> block; what it needed to be told was to write
// less per call.
func TestMalformedArgsNamesTheOutputLimit(t *testing.T) {
	c := New(t.TempDir(), &config.Model{EditFormat: "tool"})
	call := llm.ToolCall{ID: "1", Name: "write", Arguments: `{"path": "a.html", "content": "<!DOCTYPE htm`}

	c.hitOutputLimit = true
	got := c.malformedArgs(call)
	if !strings.Contains(got, "output limit") {
		t.Errorf("a truncated call is not reported as truncated:\n%s", got)
	}
	if !strings.Contains(got, "write") {
		t.Errorf("the message does not name the tool:\n%s", got)
	}

	// Without the limit signal it is a plain parse failure, and claiming the
	// output limit would be a guess the transcript does not support.
	c.hitOutputLimit = false
	if got := c.malformedArgs(call); strings.Contains(got, "output limit") {
		t.Errorf("an untruncated reply was blamed on the output limit:\n%s", got)
	}
}

// The two cases that must not be intercepted: arguments that parse, and the
// empty string a model sends for a tool that takes none — the tool's own
// parser says something more useful about what was missing.
func TestMalformedArgsLeavesUsableCallsAlone(t *testing.T) {
	c := New(t.TempDir(), &config.Model{EditFormat: "tool"})
	c.hitOutputLimit = true
	for _, args := range []string{`{"path":"a.txt","content":"hi"}`, "", "  ", "{}"} {
		if got := c.malformedArgs(llm.ToolCall{Name: "write", Arguments: args}); got != "" {
			t.Errorf("intercepted a call this pass has no business answering (%q): %s", args, got)
		}
	}
}

package repl

import (
	"bytes"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/render"
)

// TestReasoningHeaders confirms reasoning and answer stream through one
// markdown renderer, separated by the THINKING and ANSWER headers (a
// full-width rule + a bold ► label), mirroring aider.
func TestReasoningHeaders(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamReasoning("weighing options")
	o.StreamText("here is the answer")
	o.FlushStream()
	got := buf.String()

	rule := strings.Repeat("─", 40)
	for _, want := range []string{"► THINKING", "► ANSWER", "weighing options", "here is the answer", rule} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%q", want, got)
		}
	}
	if i, j := strings.Index(got, "► THINKING"), strings.Index(got, "► ANSWER"); i < 0 || j < 0 || i >= j {
		t.Errorf("THINKING must precede ANSWER (%d vs %d):\n%q", i, j, got)
	}
	if strings.Index(got, "weighing options") >= strings.Index(got, "► ANSWER") {
		t.Errorf("reasoning must appear before the ANSWER header:\n%q", got)
	}
}

// TestAnswerOnlyNoHeaders confirms a response with no reasoning shows neither
// header — aider only frames reasoning.
func TestAnswerOnlyNoHeaders(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamText("just the answer")
	o.FlushStream()
	got := buf.String()

	if strings.Contains(got, "THINKING") || strings.Contains(got, "ANSWER") {
		t.Errorf("a no-reasoning answer must not show the headers:\n%q", got)
	}
	if !strings.Contains(got, "just the answer") {
		t.Errorf("answer text missing:\n%q", got)
	}
}

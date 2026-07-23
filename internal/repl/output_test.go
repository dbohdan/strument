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

	rule := strings.Repeat("-", 40)
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

// TestWaitingClearedOnFirstOutput confirms the "Waiting for <model>" line is
// shown and then erased (CR + clear-line) before the first streamed byte.
func TestWaitingClearedOnFirstOutput(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.startWaiting("Test Model")
	o.StreamText("hi")
	o.FlushStream()
	got := buf.String()

	if !strings.Contains(got, "Waiting for Test Model") {
		t.Errorf("waiting message missing:\n%q", got)
	}
	erase := strings.Index(got, "\r\x1b[K")
	if erase < 0 {
		t.Fatalf("waiting line not erased (no CR + clear-line):\n%q", got)
	}
	if erase >= strings.Index(got, "hi") {
		t.Errorf("erase must precede the answer text:\n%q", got)
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

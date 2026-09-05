package coder

import (
	"context"
	"strings"
	"testing"
)

func toolNames(c *Coder) map[string]bool {
	names := map[string]bool{}
	for _, d := range c.toolDefs() {
		names[d.Name] = true
	}
	return names
}

// The point of the setting is that the tool is *absent*, not refused. Offering
// a capability the session will never grant is worse than withholding it: the
// model plans around something it does not have and then spends a step finding
// out, and a prompt whose answer never changes teaches that prompts are noise.
func TestNoShellWithholdsTheToolRatherThanRefusingIt(t *testing.T) {
	c := toolCoder(t, t.TempDir())

	if !toolNames(c)["bash"] {
		t.Fatal("the bash tool is not offered by default, so this test proves nothing")
	}

	c.SuggestShellCommands = false
	names := toolNames(c)
	if names["bash"] {
		t.Error("bash is still in the schema with shell disabled — the model will call it and be refused")
	}
	// Everything else must survive: this withholds one tool, not a mode.
	for _, kept := range []string{toolRead, toolGrep, toolEdit, toolWrite} {
		if !names[kept] {
			t.Errorf("tool %q disappeared along with bash", kept)
		}
	}
}

// Execution stays gated as well. The tool is the ordinary way in, but it is
// not the only one, and a withheld tool that left the runner open would be a
// setting that reads as a promise and is not one.
func TestNoShellAlsoGatesExecution(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.SuggestShellCommands = false

	// A marker that cannot occur by accident in prose. "hi" would have matched
	// the "hi" inside "this session" in the refusal itself — a substring check
	// against a natural-language message is a coin toss.
	const marker = "zqx-shell-ran-zqx"
	got := c.runShellTool(context.Background(),
		toolCommand{command: "echo " + marker, purpose: "probe"})

	if !strings.Contains(got, "disabled") {
		t.Errorf("result = %q, want the call refused with a reason", got)
	}
	if strings.Contains(got, marker+"\n") || strings.Contains(got, "\n"+marker) {
		t.Errorf("result = %q: the command ran despite shell being disabled", got)
	}
}

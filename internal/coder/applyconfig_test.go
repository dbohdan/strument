package coder

import (
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/prompts"
)

// ApplyConfig is idempotent, and a reload that drops a setting restores its
// default rather than keeping the last value.
func TestApplyConfigIsIdempotent(t *testing.T) {
	c := New(t.TempDir(), &config.Model{})
	cfg := &config.Config{SystemPrompt: "REPLACED"}

	ApplyConfig(c, cfg)
	ApplyConfig(c, cfg)
	if got := c.systemPrompt(); got != "REPLACED" {
		t.Errorf("system_prompt did not reach the active prompt:\n%.60s", got)
	}

	// And a config that stops saying restores the default rather than keeping
	// the last value, which is what a reload means.
	ApplyConfig(c, &config.Config{})
	if c.systemPrompt() != prompts.System {
		t.Error("dropping system_prompt from the config did not restore the built-in prompt")
	}
	if c.MaxSteps != defaultMaxSteps {
		t.Errorf("MaxSteps = %d after a config that omits it, want the default %d", c.MaxSteps, defaultMaxSteps)
	}
}

// --no-shell is a command-line decision a reload must not undo.
func TestApplyConfigKeepsTheShellFlag(t *testing.T) {
	c := New(t.TempDir(), &config.Model{})
	c.ShellWithheld = true
	ApplyConfig(c, &config.Config{}) // config says nothing about shell
	if c.SuggestShellCommands {
		t.Error("a reload re-enabled the bash tool that --no-shell withheld")
	}

	c2 := New(t.TempDir(), &config.Model{})
	ApplyConfig(c2, &config.Config{})
	if !c2.SuggestShellCommands {
		t.Error("without the flag the bash tool should be on")
	}
}

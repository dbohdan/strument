package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// ApplyConfig is idempotent, and the prompt layer is where that matters: a
// reload rebuilds the active set from the built-in before layering overrides.
func TestApplyConfigIsIdempotent(t *testing.T) {
	c := New(t.TempDir(), &config.Model{})
	cfg := &config.Config{
		PromptCode: "REPLACED {platform} {language} {final_reminders} {code_tools} {observation_tools}",
	}

	ApplyConfig(c, cfg)
	ApplyConfig(c, cfg)
	if !strings.HasPrefix(c.Prompts.MainSystem, "REPLACED") {
		t.Errorf("prompt_code override did not reach the active prompt:\n%.60s", c.Prompts.MainSystem)
	}

	// And a config that stops saying restores the default rather than keeping
	// the last value, which is what a reload means.
	ApplyConfig(c, &config.Config{})
	if strings.HasPrefix(c.Prompts.MainSystem, "REPLACED") {
		t.Error("dropping prompt_code from the config did not restore the built-in prompt")
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

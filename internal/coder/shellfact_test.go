package coder

import (
	"strings"
	"testing"
)

// The facts name the shell the bash tool runs, never the user's login shell,
// and only when the tool is offered.
func TestShellFactDescribesTheBuiltInShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/fish")
	c := testCoder(t)
	c.Platform = defaultPlatformInfo(c)
	c.SuggestShellCommands = true
	facts := c.factsText()
	if strings.Contains(facts, "fish") {
		t.Errorf("the facts name the login shell:\n%s", facts)
	}
	if !strings.Contains(facts, "built-in shell, which takes bash syntax") {
		t.Errorf("the facts do not say what the bash tool runs:\n%s", facts)
	}
	c.SuggestShellCommands = false
	if strings.Contains(c.factsText(), "- Shell:") {
		t.Error("a shell line without the bash tool")
	}
}

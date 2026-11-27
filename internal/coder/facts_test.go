package coder

import (
	"os"
	"path/filepath"
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

// The root is named as the root, and the directory the user started in is
// named beside it when it differs, relative when it is inside the project.
func TestFactsNameTheLaunchDirectory(t *testing.T) {
	c := testCoder(t)
	c.SuggestShellCommands = true
	c.Platform = PlatformInfo{Platform: "Linux", WorkDir: "/home/u/proj"}
	if f := c.factsText(); !strings.Contains(f, "- Project root: /home/u/proj (each bash command starts here)") ||
		strings.Contains(f, "started Strument") {
		t.Errorf("without a separate launch directory:\n%s", f)
	}
	c.Platform.LaunchDir = "/home/u/proj/internal/coder"
	if f := c.factsText(); !strings.Contains(f, "- The user started Strument in internal/coder, inside the project") {
		t.Errorf("inside the project:\n%s", f)
	}
	c.Platform.LaunchDir = "/home/u/other"
	if f := c.factsText(); !strings.Contains(f, "- The user started Strument in /home/u/other\n") {
		t.Errorf("outside the project:\n%s", f)
	}
}

// The git line names the tools this session has, and only those.
func TestFactsGitLineFollowsTheSession(t *testing.T) {
	for _, tc := range []struct {
		shell, commits bool
		want, absent   string
	}{
		{true, true, "with git through the bash tool, and commit with the commit tool rather than git commit.", ""},
		{true, false, "Commits are off for this session.", "commit tool"},
		{false, true, "- The project is a git repository. Commit with the commit tool.", "bash"},
		{false, false, "- The project is a git repository.\n", "commit"},
	} {
		c := testCoder(t)
		c.SuggestShellCommands, c.Commits = tc.shell, tc.commits
		c.Platform = PlatformInfo{Platform: "Linux", InGit: true}
		f := c.factsText() + "\n"
		_, git, found := strings.Cut(f, "- The project is")
		if !found || !strings.Contains(f, tc.want) || (tc.absent != "" && strings.Contains(git, tc.absent)) {
			t.Errorf("shell %v, commits %v:\n%s", tc.shell, tc.commits, f)
		}
	}
}

// The sandbox is stated when it confines commands, and so is a required one
// that is missing; with none, nothing is said.
func TestFactsStateTheSandbox(t *testing.T) {
	home, _ := os.UserHomeDir()
	c := testCoder(t)
	c.SuggestShellCommands = true
	c.Platform = PlatformInfo{Platform: "Linux"}
	if f := c.factsText(); strings.Contains(f, "Sandbox") || strings.Contains(f, "disabled") {
		t.Errorf("no sandbox, yet:\n%s", f)
	}
	c.Sandbox = SandboxState{Required: true, Active: true, Writable: []string{"/home/u/proj", "/tmp", filepath.Join(home, ".cache")}}
	if f := c.factsText(); !strings.Contains(f, "- Sandbox: commands can write only under /home/u/proj, /tmp, ~/.cache. Reading") {
		t.Errorf("active:\n%s", f)
	}
	c.Sandbox = SandboxState{Required: true, Unavailable: "no Landlock on this kernel: ENOSYS"}
	if f := c.factsText(); !strings.Contains(f, "- Commands are disabled: this session requires a sandbox, and none is active (no Landlock on this kernel: ENOSYS). The user can still run commands with /run.") {
		t.Errorf("required and missing:\n%s", f)
	}
	c.SuggestShellCommands = false
	if f := c.factsText(); strings.Contains(f, "Sandbox") || strings.Contains(f, "filtered environment") {
		t.Errorf("command facts without the bash tool:\n%s", f)
	}
}

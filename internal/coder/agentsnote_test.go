package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// TestAgentsNoteFollowsTheFiles: the system prompt names AGENTS.md and
// AGENTS.local.md only when they exist, and asks for them to be read rather
// than carrying their contents.
func TestAgentsNoteFollowsTheFiles(t *testing.T) {
	root := t.TempDir()
	c := New(root, &config.Model{Slug: "m"})
	system := func() string { return c.formatChatChunks().system[0].Text() }

	if got := system(); strings.Contains(got, AgentsFileName) {
		t.Fatalf("a project with no AGENTS.md must not mention one:\n%s", got)
	}

	const rule = "Every exported function gets a Contract: comment."
	if err := os.WriteFile(filepath.Join(root, AgentsFileName), []byte(rule+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := system()
	if !strings.Contains(got, AgentsFileName+" holds the project's standing instructions. Read it") {
		t.Errorf("AGENTS.md is not named as standing instructions:\n%s", got)
	}
	if strings.Contains(got, rule) || strings.Contains(got, AgentsLocalFileName) {
		t.Errorf("the prompt carries the file's contents or a file that is not there:\n%s", got)
	}

	if err := os.WriteFile(filepath.Join(root, AgentsLocalFileName), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := system(); !strings.Contains(got, AgentsLocalFileName+" holds the user's private instructions") {
		t.Errorf("AGENTS.local.md is not named:\n%s", got)
	}
}

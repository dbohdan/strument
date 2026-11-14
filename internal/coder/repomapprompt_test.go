package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
)

// TestRepoMapStaysOutOfThePrompt is the assertion behind taking it out. A
// repository with a rankable file that is not in the chat is exactly the shape
// that used to produce a repo chunk; now nothing about lib.go reaches the
// assembled messages, and the chat-files slot says plainly that no files are
// shared. The parse layer still answers when asked, now through /symbol.
func TestRepoMapStaysOutOfThePrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"),
		[]byte("package lib\n\nfunc VerySpecificName() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := &config.Model{Provider: config.Provider{Adapter: config.AdapterOpenRouter}, Slug: "test-model"}
	model.SideModel = model
	c := New(dir, model)
	c.Out = testOutput{t}
	c.RepoMap = repomap.New(dir)
	c.Repo = &fakeRepo{tracked: []string{"lib.go"}}
	c.curMessages = []llm.Message{llm.TextMessage("user", "what functions are here?")}

	var all strings.Builder
	for _, m := range c.formatChatChunks().allMessages() {
		all.WriteString(m.Text())
	}
	if strings.Contains(all.String(), "VerySpecificName") {
		t.Errorf("the repo map reached the prompt:\n%s", all.String())
	}

	// The "nothing is pinned, go and look" guidance now rides in the system
	// prompt rather than a fabricated user turn.
	var sys strings.Builder
	for _, m := range c.formatChatChunks().system {
		sys.WriteString(m.Text())
	}
	if !strings.Contains(sys.String(), "what you remember about a project is not evidence") {
		t.Errorf("system prompt:\n%q", sys.String())
	}

	// The parse layer itself still works — it just is not sent unasked. This
	// is what /map used to assert and /symbol now does: the same tags, reached
	// by the question a reader actually has.
	got, n, problem := c.SymbolLookup("VerySpecificName", "")
	if problem != "" {
		t.Fatalf("SymbolLookup: %s", problem)
	}
	if n != 1 || !strings.Contains(got, "lib.go:3") {
		t.Errorf("/symbol should find the definition, got %d site(s):\n%s", n, got)
	}
}

// /ask mode: an edit format whose engine parses nothing. These pin the
// four behaviors Opus flagged — empty examples chunk (cache breakpoint
// falls back to system), the falsy-sentinel repo-map branch, and a
// SEARCH/REPLACE block plus a shell fence in an ask answer being left as
// prose (no edits, no commit, no shell run).

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
)

func askCoder(t *testing.T, dir string) *Coder {
	t.Helper()
	model := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "diff",
		RepoMap:    true,
	}
	model.WeakModel = model
	c := New(dir, model)
	c.Out = testOutput{t}
	c.Confirm = yesConfirmer{}
	c.SetEditFormat("ask")
	return c
}

func TestAskAssemblyNoExamplesReminderPresent(t *testing.T) {
	c := askCoder(t, t.TempDir())
	c.Platform.Language = "English"
	c.ReminderPlacement = "sys"
	c.curMessages = []llm.Message{llm.TextMessage("user", "what does this do?")}

	chunks := c.formatChatChunks()

	if len(chunks.examples) != 0 {
		t.Errorf("ask must have no few-shot examples chunk, got %d", len(chunks.examples))
	}
	if len(chunks.reminder) != 1 || !strings.Contains(chunks.reminder[0].Text(), "Reply in English") {
		t.Errorf("ask reminder chunk = %+v", chunks.reminder)
	}
	var sys strings.Builder
	for _, m := range chunks.system {
		sys.WriteString(m.Text())
	}
	if !strings.Contains(sys.String(), "expert code analyst") {
		t.Errorf("ask system prompt not active:\n%s", sys.String())
	}
}

func TestAskCacheBreakpointFallsBackToSystem(t *testing.T) {
	c := askCoder(t, t.TempDir())
	c.CacheHeaders = true
	p := filepath.Join(c.Root, "a.txt")
	if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("a.txt")
	c.curMessages = []llm.Message{llm.TextMessage("user", "explain a.txt")}

	chunks := c.formatMessages()

	count := func(msgs []llm.Message) int {
		n := 0
		for _, m := range msgs {
			for _, b := range m.Content.Blocks {
				if b.CacheControl != nil {
					n++
				}
			}
		}
		return n
	}
	// No examples chunk in ask mode, so the examples-else-system breakpoint
	// lands on system.
	if got := count(chunks.examples); got != 0 {
		t.Errorf("examples breakpoints = %d, want 0 (no examples in ask)", got)
	}
	if got := count(chunks.system); got != 1 {
		t.Errorf("system breakpoints = %d, want 1 (fallback)", got)
	}
}

func TestAskFalsySentinelUsesPlainNoFullFiles(t *testing.T) {
	dir := t.TempDir()
	// A repo file that is NOT in the chat, so the repo map has content but
	// there are no editable files — the branch the sentinel governs.
	if err := os.WriteFile(filepath.Join(dir, "lib.go"),
		[]byte("package lib\n\nfunc Exported() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := askCoder(t, dir)
	rm := repomap.New(dir)
	rm.MaxContextWindow = 4096
	rm.RepoContentPrefix = c.Prompts.RepoContentPrefix
	c.RepoMap = rm
	// A repo that lists lib.go as tracked, so the map can rank it even
	// though it is not in the chat (no git needed for the assembly path).
	c.Repo = &fakeRepo{tracked: []string{"lib.go"}}
	c.curMessages = []llm.Message{llm.TextMessage("user", "what functions are here?")}

	chunks := c.formatChatChunks()
	if len(chunks.repo) == 0 {
		t.Skip("repo map produced no content in this environment; sentinel branch unreachable")
	}

	var chat strings.Builder
	for _, m := range chunks.chatFiles {
		chat.WriteString(m.Text())
	}
	got := chat.String()
	// Ask's files_no_full_files_with_repo_map is "" (falsy sentinel), so
	// the branch is disabled and the plain files_no_full_files is used —
	// not an empty pair, not a skipped chunk.
	if !strings.Contains(got, "I am not sharing the full contents of any files with you yet") {
		t.Errorf("falsy sentinel not honored; chat_files chunk:\n%q", got)
	}
}

// recordingRepo counts commits; predicates are inert (clean, in-repo).
type recordingRepo struct {
	root        string
	commitCalls int
}

func (r *recordingRepo) Root() string           { return r.root }
func (r *recordingRepo) TrackedFiles() []string { return nil }
func (r *recordingRepo) PathInRepo(string) bool { return true }
func (r *recordingRepo) IsDirty(string) bool    { return false }
func (r *recordingRepo) GitIgnored(string) bool { return false }
func (r *recordingRepo) HeadSHA() string        { return "deadbeef" }
func (r *recordingRepo) Commit([]string, string, bool) (string, string, bool, error) {
	r.commitCalls++
	return "abc1234", "msg", true, nil
}

// recordingRunner counts shell executions.
type recordingRunner struct{ calls int }

func (r *recordingRunner) Run(context.Context, string, string) (int, string, error) {
	r.calls++
	return 0, "", nil
}

func TestAskModeIgnoresSearchReplaceAndShell(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := askCoder(t, dir)
	c.AddFile("main.txt")

	repo := &recordingRepo{root: dir}
	c.Repo = repo
	c.AutoCommits = true
	runner := &recordingRunner{}
	c.Runner = runner

	// An ask answer that (mischievously) contains a well-formed
	// SEARCH/REPLACE block and a shell fence. Both must be left as prose.
	answer := "You could change it like this:\n\n" +
		"main.txt\n```\n<<<<<<< SEARCH\nhello world\n=======\nhello strument\n>>>>>>> REPLACE\n```\n\n" +
		"Then run:\n\n```bash\nrm -rf /\n```\n"
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{{Events: []fixture.Event{
		{Kind: "Answer", Text: answer},
		{Kind: "Finish", FinishReason: "stop"},
	}}}}

	c.Run(context.Background(), "how would I change the greeting?")

	// The file is untouched.
	if got, _ := os.ReadFile(filepath.Join(dir, "main.txt")); string(got) != "hello world\n" {
		t.Errorf("ask mode edited the file: %q", got)
	}
	if repo.commitCalls != 0 {
		t.Errorf("ask mode committed %d times", repo.commitCalls)
	}
	if runner.calls != 0 {
		t.Errorf("ask mode ran %d shell commands", runner.calls)
	}
	// History has the plain Q&A, no "committed" rotation.
	if len(c.doneMessages) != 0 {
		t.Errorf("ask mode rotated history: %s", dumpHistory(c.doneMessages))
	}
}

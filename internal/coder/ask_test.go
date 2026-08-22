// /ask mode: an edit format whose engine parses nothing. These pin the
// four behaviors Opus flagged — empty examples chunk (cache breakpoint
// falls back to system), the falsy-sentinel repo-map branch, and a
// SEARCH/REPLACE block plus a shell fence in an ask answer being left as
// prose (no edits, no commit, no shell run).

package coder

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	model.SideModel = model
	c := New(dir, model)
	c.Out = testOutput{t}
	c.Confirm = yesConfirmer{}
	c.SetEditFormat("ask")
	return c
}

func TestAskAssemblyNoExamplesReminderPresent(t *testing.T) {
	c := askCoder(t, t.TempDir())
	c.Platform.Language = "English"
	c.curMessages = []llm.Message{llm.TextMessage("user", "what does this do?")}

	chunks := c.formatChatChunks()

	if len(chunks.examples) != 0 {
		t.Errorf("ask must have no few-shot examples chunk, got %d", len(chunks.examples))
	}
	if !strings.Contains(chunks.system[0].Text(), "Reply in English") {
		t.Errorf("ask system prompt lost the language reminder:\n%s", chunks.system[0].Text())
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
	c.Model.Cache = true
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
	c := askCoder(t, dir)
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
	if !strings.Contains(sys.String(), "answer from what you find there rather than from memory") {
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
func (r *recordingRepo) Commit([]string, string, string, bool) (string, string, bool, error) {
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
	// History is the plain Q&A and nothing else. This used to assert that
	// doneMessages stayed empty, because rotation was what carried aider's
	// synthetic "I applied and committed your changes" pair — the comment said
	// so: no "committed" rotation. The pair is gone, and every turn settles into
	// done now, so the assertion is written against what it was always about.
	hist := history(c)
	if len(hist) != 2 {
		t.Errorf("history should be exactly the question and the answer: %s", dumpHistory(hist))
	}
	for _, m := range hist {
		if strings.Contains(m.Text(), "committed your changes") || strings.TrimSpace(m.Text()) == "Ok." {
			t.Errorf("ask mode fabricated a turn: %s", dumpHistory(hist))
		}
	}
}

// Ask mode's request must actually carry its tools. It did not: buildRequest
// gated req.Tools on editFormat == "tool", so ask sent none, and toolDefs's own
// "ask" branch was unreachable — the source read as though ask had a read-only
// tool set while the wire carried nothing. A live pass is what caught it, so
// this pins the wire rather than the tool list.
func TestAskRequestCarriesReadOnlyTools(t *testing.T) {
	c := askCoder(t, t.TempDir())
	c.RepoMap = repomap.New(c.Root)
	req := c.buildRequest([]llm.Message{llm.TextMessage("user", "what is here?")})

	if len(req.Tools) == 0 {
		t.Fatal("ask mode sent no tools at all")
	}
	if req.ToolChoice != "auto" {
		t.Errorf("tool_choice = %q, want auto", req.ToolChoice)
	}
	got := map[string]bool{}
	for _, d := range req.Tools {
		got[d.Name] = true
	}
	for _, want := range []string{"read", "grep", "glob", "ls", "symbol", "ask_user_question"} {
		if !got[want] {
			t.Errorf("ask mode should offer %s, got %v", want, slices.Sorted(maps.Keys(got)))
		}
	}
	// The withheld half is what makes it ask mode, and it is withheld by the
	// tool set rather than by the prompt.
	for _, gone := range []string{"edit", "write", "bash", "check"} {
		if got[gone] {
			t.Errorf("ask mode must not offer %s", gone)
		}
	}
}

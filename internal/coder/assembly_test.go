// Assembly invariants: reminder gate paths, fence escalation, unreadable
// chat files, cache breakpoints, reasoning strip (basecoder-spec §3, §5).

package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbohdan/strument/internal/config"
	"github.com/dbohdan/strument/internal/llm"
)

func testCoder(t *testing.T) *Coder {
	t.Helper()
	model := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "diff",
	}
	model.WeakModel = model
	c := New(t.TempDir(), model)
	c.Out = testOutput{t}
	c.Confirm = yesConfirmer{}
	return c
}

func TestReminderUserPath(t *testing.T) {
	c := testCoder(t)
	c.ReminderPlacement = "user"
	c.curMessages = []llm.Message{llm.TextMessage("user", "do the thing")}
	chunks := c.formatChatChunks()
	if len(chunks.reminder) != 0 {
		t.Error("user path must leave the reminder slot empty")
	}
	final := chunks.cur[len(chunks.cur)-1]
	if !strings.Contains(final.Text(), "SEARCH/REPLACE block* Rules") || !strings.HasPrefix(final.Text(), "do the thing\n\n") {
		t.Errorf("final user message = %q...", final.Text()[:80])
	}
	// The reminder is stitched into the outgoing clone only, never history.
	if c.curMessages[0].Text() != "do the thing" {
		t.Errorf("history mutated: %q", c.curMessages[0].Text())
	}
}

func TestReminderSysPath(t *testing.T) {
	c := testCoder(t)
	c.ReminderPlacement = "sys"
	c.curMessages = []llm.Message{llm.TextMessage("user", "do the thing")}
	chunks := c.formatChatChunks()
	if len(chunks.reminder) != 1 || chunks.reminder[0].Role != "system" {
		t.Fatalf("reminder slot = %+v", chunks.reminder)
	}
	final := chunks.cur[len(chunks.cur)-1]
	if final.Text() != "do the thing" {
		t.Errorf("user message must stay clean on the sys path: %q", final.Text())
	}
}

func TestReminderUserPathSkippedWhenFinalIsAssistant(t *testing.T) {
	c := testCoder(t)
	c.ReminderPlacement = "user"
	c.curMessages = []llm.Message{
		llm.TextMessage("user", "hello"),
		llm.TextMessage("assistant", "partial reply"),
	}
	chunks := c.formatChatChunks()
	final := chunks.cur[len(chunks.cur)-1]
	if final.Text() != "partial reply" {
		t.Errorf("assistant-final message must not get the reminder: %q", final.Text())
	}
	if len(chunks.reminder) != 0 {
		t.Error("reminder slot must stay empty on the user path")
	}
}

func TestReminderUnknownMaxAlwaysAdds(t *testing.T) {
	c := testCoder(t)
	c.Model.Context = 0 // unknown
	c.ReminderPlacement = "sys"
	c.curMessages = []llm.Message{llm.TextMessage("user", "x")}
	if chunks := c.formatChatChunks(); len(chunks.reminder) != 1 {
		t.Error("unknown max_input_tokens must always add the reminder")
	}

	c.Model.Context = 100 // tiny known window: gate blocks
	if chunks := c.formatChatChunks(); len(chunks.reminder) != 0 {
		t.Error("gate must block the reminder when over budget")
	}
}

func TestFenceEscalationWhenChatFileHasBackticks(t *testing.T) {
	c := testCoder(t)
	p := filepath.Join(c.Root, "doc.md")
	if err := os.WriteFile(p, []byte("intro\n```go\ncode\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("doc.md")
	c.chooseFence()
	if c.fence.open != "````" {
		t.Errorf("fence = %q, want quad backticks", c.fence.open)
	}
	// The system prompt gains the quad-backtick reminder.
	sys := c.fmtSystemPrompt(c.Prompts.SystemReminder)
	if !strings.Contains(sys, "IMPORTANT: Use *quadruple* backticks") {
		t.Error("quad backtick reminder missing")
	}
}

func TestUnreadableChatFileDroppedDuringChooseFence(t *testing.T) {
	c := testCoder(t)
	good := filepath.Join(c.Root, "good.txt")
	if err := os.WriteFile(good, []byte("fine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("good.txt")
	c.AddFile("gone.txt") // never created
	c.chooseFence()
	if len(c.absFnames) != 1 || c.relFname(c.absFnames[0]) != "good.txt" {
		t.Errorf("absFnames = %v", c.absFnames)
	}
}

func TestCacheBreakpointsSnapshot(t *testing.T) {
	c := testCoder(t)
	c.CacheHeaders = true
	p := filepath.Join(c.Root, "a.txt")
	if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("a.txt")
	c.doneMessages = []llm.Message{llm.TextMessage("user", "old"), llm.TextMessage("assistant", "old reply")}
	c.curMessages = []llm.Message{llm.TextMessage("user", "new")}

	chunks := c.formatMessages()

	countBreakpoints := func(msgs []llm.Message) int {
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
	// examples slot exists (editblock examples) => breakpoint there, none
	// on system; readonly empty and repo empty => that breakpoint is a
	// no-op; chat_files gets one.
	if got := countBreakpoints(chunks.examples); got != 1 {
		t.Errorf("examples breakpoints = %d", got)
	}
	if got := countBreakpoints(chunks.system); got != 0 {
		t.Errorf("system breakpoints = %d", got)
	}
	if got := countBreakpoints(chunks.chatFiles); got != 1 {
		t.Errorf("chat_files breakpoints = %d", got)
	}
	if got := countBreakpoints(chunks.done) + countBreakpoints(chunks.cur); got != 0 {
		t.Errorf("done/cur breakpoints = %d (never cacheable)", got)
	}
	// Decoration must not mutate history.
	for _, m := range c.doneMessages {
		if m.Content.Blocks != nil {
			t.Error("doneMessages mutated by cache decoration")
		}
	}
}

func TestStripReasoningInlineAndNative(t *testing.T) {
	// Complete tag pair.
	if got := stripReasoning("<think>secret plan</think>\n\nanswer", "think"); got != "answer" {
		t.Errorf("got %q", got)
	}
	// Lone closing tag: keep the tail.
	if got := stripReasoning("half thoughts</think>real answer", "think"); got != "real answer" {
		t.Errorf("got %q", got)
	}
	// Empty tag => no strip.
	if got := stripReasoning("<think>keep</think> me", ""); got != "<think>keep</think> me" {
		t.Errorf("got %q", got)
	}
	// Regex metacharacters in the tag are quoted.
	if got := stripReasoning("<th.ink+>x</th.ink+>done", "th.ink+"); got != "done" {
		t.Errorf("got %q", got)
	}
	// DOTALL: reasoning spans lines.
	if got := stripReasoning("<think>line1\nline2</think>after", "think"); got != "after" {
		t.Errorf("got %q", got)
	}
}

func TestNativeReasoningNeverReachesAnswer(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Reasoning","text":"private"},{"kind":"Answer","text":"<think>inline</think>public"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, func(c *Coder) { c.Model.ReasoningTag = "think" })
	env.run(t)
	if env.coder.partialResponseContent != "public" {
		t.Errorf("answer = %q", env.coder.partialResponseContent)
	}
	if env.coder.partialReasoningContent != "private" {
		t.Errorf("reasoning buffer = %q", env.coder.partialReasoningContent)
	}
	hist := env.coder.curMessages
	if len(hist) != 2 || hist[1].Text() != "public" {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestPyFormat(t *testing.T) {
	got := pyFormat("a {x} b {{literal}} c {fence[0]} d {missing}", map[string]string{
		"x": "X", "fence[0]": "```",
	})
	want := "a X b {literal} c ``` d {missing}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

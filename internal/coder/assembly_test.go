// Assembly invariants: reminder gate paths, fence escalation, unreadable
// chat files, cache breakpoints, reasoning strip.

package coder

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

func testCoder(t *testing.T) *Coder {
	t.Helper()
	model := &config.Model{
		Provider: config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:     "test-model",
	}
	c := New(t.TempDir(), model)
	c.Out = testOutput{t}
	c.Confirm = yesConfirmer{}
	return c
}

// The editing rules go out exactly once, in the system prompt.
//
// They used to go out twice: aider appends the reminder again at the end, and
// Strument carried that. Claude Haiku reported the duplication when asked what
// looked odd about the harness, and a probe confirmed two copies per send. This
// pins the count rather than the mechanism, so a future re-introduction of a
// second copy fails here whatever shape it takes.
func TestEditingRulesAppearOnce(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{llm.TextMessage("user", "do the thing")}

	for turn := 1; turn <= 3; turn++ {
		var all strings.Builder
		for _, m := range c.formatChatChunks().allMessages() {
			all.WriteString(m.Text())
		}
		if n := strings.Count(all.String(), "# Editing rules"); n != 1 {
			t.Errorf("turn %d: the editing rules appear %d times, want 1", turn, n)
		}
		c.moveBackCurMessages()
		c.curMessages = []llm.Message{llm.TextMessage("user", "again")}
	}
}

// The user's own words are the user's. The retired "user" placement edited the
// reminder into the last user message, which is one of the places the harness
// pretended the user had said something.
func TestTheUserMessageIsLeftAlone(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{llm.TextMessage("user", "do the thing")}

	chunks := c.formatChatChunks()
	final := chunks.cur[len(chunks.cur)-1]
	if final.Text() != "do the thing" {
		t.Errorf("the outgoing user message was edited: %q", final.Text())
	}
	if c.curMessages[0].Text() != "do the thing" {
		t.Errorf("history mutated: %q", c.curMessages[0].Text())
	}
	if !strings.Contains(chunks.system[0].Text(), "# Editing rules") {
		t.Error("the rules should be in the system prompt")
	}
}

// A tiny context window no longer drops the rules. The old gate weighed the
// reminder against the budget because it was a second copy that could be
// skipped; the only copy cannot be, and a window too small for the system
// prompt is a window too small for the session.
func TestRulesSurviveATinyWindow(t *testing.T) {
	c := testCoder(t)
	c.Model.Context = 100
	c.curMessages = []llm.Message{llm.TextMessage("user", "x")}
	if !strings.Contains(c.formatChatChunks().system[0].Text(), "# Editing rules") {
		t.Error("the rules were dropped on a tiny window")
	}
}

func TestCacheBreakpointsSnapshot(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
	p := filepath.Join(c.Root, "a.txt")
	if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	// One on the system prompt. readonly is empty here, so that breakpoint
	// is a no-op. chat_files is gone: pinned files are named in the system
	// prompt now, so there is no per-turn block left to cache separately.
	if got := countBreakpoints(chunks.system); got != 1 {
		t.Errorf("system breakpoints = %d, want 1", got)
	}
	// Two roll with the conversation: on the request's last message, and on
	// the message before the last assistant turn, where the previous
	// request's last breakpoint was.
	if got := countBreakpoints(chunks.done) + countBreakpoints(chunks.cur); got != 2 {
		t.Errorf("done/cur breakpoints = %d, want 2", got)
	}
	if countBreakpoints(chunks.cur[:1]) != 1 || countBreakpoints(chunks.done[:1]) != 1 || countBreakpoints(chunks.done[1:2]) != 0 {
		t.Errorf("breakpoints on the wrong messages: done %+v, cur %+v", chunks.done, chunks.cur)
	}
	// Decoration must not mutate history.
	for _, m := range c.doneMessages {
		if m.Content.Blocks != nil {
			t.Error("doneMessages mutated by cache decoration")
		}
	}
}

// TestConversationBreakpointKeepsBlocks: a tool result with an image keeps its
// image when it carries the rolling breakpoint, and the history's own block
// slice is not touched — done aliases it.
func TestConversationBreakpointKeepsBlocks(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
	img := llm.ImageBlock(llm.ImageSource{MediaType: "image/png", Data: "AAAA", Label: "x.png"})
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: llm.BlocksContent(llm.TextBlock("read x.png"), img)}
	c.doneMessages = []llm.Message{llm.TextMessage("user", "look"), {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read"}}}, result}
	c.curMessages = nil

	chunks := c.formatMessages()

	last := chunks.done[2].Content.Blocks
	if len(last) != 2 || last[1].Type != llm.BlockImage || last[1].CacheControl == nil {
		t.Errorf("marked tool result = %+v, want the image kept and marked", last)
	}
	if c.doneMessages[2].Content.Blocks[1].CacheControl != nil {
		t.Error("the history's block slice was marked in place")
	}
	// The assistant turn is only tool calls: nothing on it to mark, and the
	// message before it carries the second breakpoint.
	if chunks.done[0].Content.Blocks == nil || chunks.done[0].Content.Blocks[0].CacheControl == nil {
		t.Errorf("message before the assistant turn not marked: %+v", chunks.done[0])
	}
}

func TestCacheBreakpointTTL(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
	p := filepath.Join(c.Root, "a.txt")
	if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.curMessages = []llm.Message{llm.TextMessage("user", "new")}

	chunks := c.formatMessages()

	found := 0
	for _, m := range chunks.allMessages() {
		for _, b := range m.Content.Blocks {
			if b.CacheControl == nil {
				continue
			}
			found++
			if b.CacheControl.Type != "ephemeral" || b.CacheControl.TTL != "1h" {
				t.Errorf("cache_control = %+v, want ephemeral/1h", *b.CacheControl)
			}
		}
	}
	if found == 0 {
		t.Fatal("no cache breakpoints were decorated")
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
	hist := history(env.coder)
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

// The {code_tools} slot tracks the schema, in both directions. The code-mode
// trial found 0/36 uptake with the tool offered in the schema but absent from
// the prompt's tool list, which models read as closed-world
// (doc/experiments/2026-08-code-mode/README.md). The bullet is therefore filled from
// the same condition that offers the tool. The reverse direction matters too:
// a feature-reverted binary (the trial's arm A) must not name a tool the
// schema withholds.
func TestCodeToolsSlotTracksTheSchema(t *testing.T) {
	c := testCoder(t)
	if !strings.Contains(c.fmtSystemPrompt(prompts.Tool.MainSystem), "run_code runs a short JavaScript program") {
		t.Error("the run_code tool is offered, but the prompt does not mention it")
	}

	// A coder built with the run_code tool withheld: the slot must come back empty.
	withheld := testCoder(t)
	withheld.OfferCode = false
	sys := withheld.fmtSystemPrompt(prompts.Tool.MainSystem)
	if strings.Contains(sys, "run_code runs a short JavaScript program") {
		t.Error("the run_code tool is withheld, but the prompt names it")
	}
}

// prompt_code replaces MainSystem, in the session's copy only.
func TestPromptCodeOverridesMainSystem(t *testing.T) {
	c := testCoder(t)
	c.PromptCode = "You are the code-format prompt.\n{final_reminders}\n"
	c.setPrompts()

	if got := c.fmtSystemPrompt(c.Prompts.MainSystem); !strings.Contains(got, "code-format prompt") {
		t.Errorf("tool-mode system prompt is not the override:\n%s", got)
	}
	if strings.Contains(c.fmtSystemPrompt(prompts.Tool.MainSystem), "code-format prompt") {
		t.Error("the built-in singleton was mutated, not the session copy")
	}
}

// chat_language refills the {language} and {final_reminders} slots, replacing
// the env-derived language without touching anything else in the platform.
func TestChatLanguageFillsSlots(t *testing.T) {
	c := testCoder(t)
	c.SetChatLanguage("fr")

	if !strings.Contains(c.fmtSystemPrompt(prompts.Tool.MainSystem), "Reply in French.") {
		t.Error("the configured language did not reach {final_reminders}")
	}
	if c.Platform.Language != "French" {
		t.Errorf("Platform.Language = %q, want French", c.Platform.Language)
	}
}

// prompt_system_prefix lands before the assembled system prompt via
// SystemPromptPrefix, the Tier 0 literal injector with no placeholder
// substitution.
func TestPromptSystemPrefixPrepends(t *testing.T) {
	c := testCoder(t)
	c.SystemPromptPrefix = "Be terse."

	all := formatAllSystem(c.formatChatChunks())
	if !strings.HasPrefix(all, "Be terse.") {
		t.Errorf("system prompt does not open with the prefix:\n%s", all)
	}
}

func formatAllSystem(chunks *chatChunks) string {
	var b strings.Builder
	for _, m := range chunks.system {
		b.WriteString(m.Text())
	}
	return b.String()
}

// TestConversationShapeIsStable: with caching on, an unmarked user or tool
// message goes out as blocks too, so a message has one shape whether or not
// it carries a breakpoint this step. A live run showed why: plain text one
// step and a marked block the next cost MiMo-V2.6-Flash, whose provider caches
// prefixes on its own, about a tenth of its cache hits. Assistant turns keep
// their shape; they are never marked.
func TestConversationShapeIsStable(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
	c.doneMessages = []llm.Message{
		llm.TextMessage("user", "one"), llm.TextMessage("assistant", "reply one"),
		llm.TextMessage("user", "two"), llm.TextMessage("assistant", "reply two"),
	}
	c.curMessages = []llm.Message{llm.TextMessage("user", "three")}
	chunks := c.formatMessages()

	unmarked := chunks.done[0] // neither the tail nor the message before the last answer
	if unmarked.Content.Text != nil || len(unmarked.Content.Blocks) != 1 || unmarked.Content.Blocks[0].CacheControl != nil {
		t.Errorf("unmarked user message = %+v, want one unmarked block", unmarked.Content)
	}
	if chunks.done[1].Content.Text == nil {
		t.Errorf("assistant turn reshaped: %+v", chunks.done[1].Content)
	}

	// Off, nothing is reshaped.
	c.Model.Cache = false
	if got := c.formatMessages().done[0].Content; got.Text == nil {
		t.Errorf("with caching off, a user message was reshaped: %+v", got)
	}
}

// TestGeminiBreakpointOnLastUserMessage: a cache that uses only the request's
// last breakpoint (Gemini, per OpenRouter) gets one conversation breakpoint,
// on the last user message — the turn's own request, which stays put while
// the turn's tool results accumulate after it.
func TestGeminiBreakpointOnLastUserMessage(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
	c.Model.Slug = "google/gemini-3.8-flash"
	c.doneMessages = []llm.Message{llm.TextMessage("user", "earlier"), llm.TextMessage("assistant", "done")}
	c.curMessages = []llm.Message{
		llm.TextMessage("user", "this turn"),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read"}}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: llm.TextContent("file contents")},
	}
	chunks := c.formatMessages()

	marked := func(m llm.Message) bool {
		for _, b := range m.Content.Blocks {
			if b.CacheControl != nil {
				return true
			}
		}
		return false
	}
	var got []string
	for _, m := range append(slices.Clone(chunks.done), chunks.cur...) {
		if marked(m) {
			got = append(got, m.Role+":"+m.Content.String())
		}
	}
	if strings.Join(got, "|") != "user:this turn" {
		t.Errorf("conversation breakpoints = %q, want only the turn's own request", got)
	}
	for _, slug := range []string{"anthropic/claude-haiku-5.5", "xiaomi/mimo-v2.6-flash"} {
		if lastBreakpointOnly(slug) {
			t.Errorf("%s treated as last-breakpoint-only", slug)
		}
	}
}

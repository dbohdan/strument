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
	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

func testCoder(t *testing.T) *Coder {
	t.Helper()
	model := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "tool",
	}
	model.SideModel = model
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
	// The escalation no longer reaches the prompt — fences framed SEARCH/REPLACE
	// blocks, and those are gone. It still matters for the fenced did-you-mean
	// an unmatched edit returns, which must not be cut short by a fence the
	// file's own content already uses.
	quoted := editblock.FindSimilarLines("```go\ncode\n```\n", "intro\n```go\ncode\n```\n", 0.6)
	if quoted == "" {
		t.Fatal("did-you-mean found nothing to quote")
	}
	if strings.HasPrefix(quoted, c.fence.open) {
		t.Error("the chosen fence must not collide with the content it wraps")
	}
}

func TestChatFileRetentionDuringChooseFence(t *testing.T) {
	c := testCoder(t)
	if err := os.WriteFile(filepath.Join(c.Root, "good.txt"), []byte("fine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.Root, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.AddFile("good.txt")
	c.AddFile("gone.txt") // never created: a to-be-created file, kept as empty
	c.AddFile("adir")     // a directory reads as a non-ENOENT error: dropped
	c.chooseFence()

	kept := map[string]bool{}
	for _, f := range c.absFnames {
		kept[c.relFname(f)] = true
	}
	if !kept["good.txt"] || !kept["gone.txt"] || kept["adir"] {
		t.Errorf("absFnames = %v (want good.txt + gone.txt, not adir)", c.absFnames)
	}
}

// A pinned file that does not exist yet is named as one to create. Telling the
// model to read it would send it after something that is not there — the case
// the A0/A2 run never covered, because every fixture file existed.
func TestNonexistentPinnedFileIsNamedAsOneToCreate(t *testing.T) {
	c := testCoder(t)
	c.AddFile("new.go") // never created
	note := c.pinnedFilesNote()
	if !strings.Contains(note, "new.go") || !strings.Contains(note, "does not exist yet") {
		t.Errorf("nonexistent file not offered for creation:\n%q", note)
	}
	if strings.Contains(note, "Read them before editing") {
		t.Errorf("the model was told to read a file that is not there:\n%q", note)
	}
	if !slices.Contains(c.absFnames, c.absRootPath("new.go")) {
		t.Errorf("nonexistent file was dropped from the chat: %v", c.absFnames)
	}
}

func TestCacheBreakpointsSnapshot(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
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
	// No prompt set ships few-shot examples any more — the schema carries the
	// format — so the examples slot is empty and its breakpoint falls back to
	// system, which is the documented fallback in addCacheControlHeaders.
	// readonly is empty here, so that breakpoint is a no-op. chat_files is
	// gone: pinned files are named in the system prompt now, so there is no
	// per-turn block left to cache separately.
	if got := countBreakpoints(chunks.examples); got != 0 {
		t.Errorf("examples breakpoints = %d, want 0 (no examples ship now)", got)
	}
	if got := countBreakpoints(chunks.system); got != 1 {
		t.Errorf("system breakpoints = %d, want 1 (the examples fallback)", got)
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

func TestCacheBreakpointTTL(t *testing.T) {
	c := testCoder(t)
	c.Model.Cache = true
	p := filepath.Join(c.Root, "a.txt")
	if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("a.txt")
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

// The note is read by a model, so it has to read like a sentence. A wire
// capture caught "pinned 1 file … These are the files" and "Create them" for a
// single file, so the counted forms are pinned here.
//
// The sentence that used to carry most of that risk is gone: the instruction to
// read is now generic-singular ("Read a file before …"), which agrees with
// itself whatever the count and leaves nothing to get wrong.
func TestPinnedFilesNoteAgreesInNumber(t *testing.T) {
	one := testCoder(t)
	if err := os.WriteFile(filepath.Join(one.Root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	one.AddFile("a.go")
	one.AddFile("gone.go") // never created
	got := one.pinnedFilesNote()
	for _, want := range []string{"pinned 1 file", "1 file that does not exist yet",
		"Create it with write"} {
		if !strings.Contains(got, want) {
			t.Errorf("singular form missing %q:\n%s", want, got)
		}
	}

	many := testCoder(t)
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(many.Root, f), []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		many.AddFile(f)
	}
	got = many.pinnedFilesNote()
	for _, want := range []string{"pinned 2 files"} {
		if !strings.Contains(got, want) {
			t.Errorf("plural form missing %q:\n%s", want, got)
		}
	}
}

// Pinning a file is not a request to change it.
//
// The note used to say "These are the files they want changed", which asserts
// an intention the act of pinning does not carry — a user pins files to put
// them in front of the model, and the message says what to do with them.
// GLM-5.3 was caught reconciling the two out loud: "the pinned files are for
// changes... but the user question is analysis/proposal", then talking itself
// back to answering the question actually asked. The prompt made it argue
// against the prompt.
func TestPinnedFilesNoteClaimsNoIntent(t *testing.T) {
	c := testCoder(t)
	if err := os.WriteFile(filepath.Join(c.Root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("a.go")

	got := c.pinnedFilesNote()
	// The claim, not the words. A first draft of this check also forbade "they
	// want", and the sentence that *denies* the claim — "the user's message
	// says what they want" — tripped it. Same fault as the scorers in
	// doc/experimenting.md, one layer up: a pattern matching text present for
	// the opposite reason.
	for _, unwanted := range []string{"want changed", "wants changed"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the note asserts what the user wants (%q):\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "not itself a request") {
		t.Errorf("the note does not say pinning is not a request:\n%s", got)
	}
}

// Ask mode has no editing tools, so the note must not talk about editing or
// tell the model to create anything.
//
// The whole note was mode-blind: a session that could not change a byte was
// told these were the files the user wanted changed, and that a missing one
// should be created "with write" — a tool not in its schema. An instruction
// the model cannot follow is worse than none, because it has to work out that
// it cannot follow it.
func TestPinnedFilesNoteSuitsAskMode(t *testing.T) {
	c := testCoder(t)
	c.editFormat = "ask"
	if err := os.WriteFile(filepath.Join(c.Root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddFile("a.go")
	c.AddFile("gone.go") // never created

	got := c.pinnedFilesNote()
	for _, unwanted := range []string{"with write", "changing it", "before editing"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("ask mode note mentions editing (%q):\n%s", unwanted, got)
		}
	}
	// The note used to be mode-aware here, saying "before changing it" in code
	// mode and "before answering questions about it" in ask. It no longer needs
	// to be: "before you work on it" is true in both, and the branch was the
	// only thing making one wording wrong in the other mode. What must survive
	// is the instruction itself — 600 samples took blind edits from 383 to zero
	// on the strength of it (2026-08-add-instruct.md).
	if !strings.Contains(got, "read a file before you work on it") {
		t.Errorf("ask mode note no longer tells the model to read:\n%s", got)
	}

	// ...and code mode still does say it, or the branch is just deleting text.
	code := testCoder(t)
	if err := os.WriteFile(filepath.Join(code.Root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code.AddFile("a.go")
	code.AddFile("gone.go")
	if got := code.pinnedFilesNote(); !strings.Contains(got, "Create it with write") {
		t.Errorf("code mode lost its create instruction:\n%s", got)
	}
}

// The {code_tools} slot tracks the schema, in both directions. The code-mode
// trial found 0/36 uptake with the tool offered in the schema but absent from
// the prompt's tool list, which models read as closed-world
// (doc/experiments/2026-08-code-mode.md). The bullet is therefore filled from
// the same condition that offers the tool. The reverse direction matters too:
// a feature-reverted binary (the trial's arm A) must not name a tool the
// schema withholds.
func TestCodeToolsSlotTracksTheSchema(t *testing.T) {
	c := testCoder(t)
	if !strings.Contains(c.fmtSystemPrompt(prompts.Tool.MainSystem), "code runs a short Python program") {
		t.Error("the code tool is offered, but the prompt does not mention it")
	}

	// A coder built with the code tool withheld: the slot must come back empty.
	withheld := testCoder(t)
	withheld.OfferCode = false
	sys := withheld.fmtSystemPrompt(prompts.Tool.MainSystem)
	if strings.Contains(sys, "code runs a short Python program") {
		t.Error("the code tool is withheld, but the prompt names it")
	}
}

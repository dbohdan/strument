package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// A deterministic render of the fold the model reads. The coder is given a
// synthetic compaction summary (a marked harness turn carrying SummaryLabel)
// sitting in settled history, and a live turn on top of it; the view must show
// the summary in order, then the live tail.
func TestViewContextRendersSummaryAndTail(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.HarnessNote(prompts.SummaryLabel + "EARLIER WORK"),
		llm.TextMessage("user", "did someone fix the poll loop?"),
		llm.TextMessage("assistant", "yes, done"),
	}
	c.curMessages = []llm.Message{
		llm.TextMessage("user", "ok"),
		llm.TextMessage("assistant", "good"),
	}

	got := c.ViewContext(-1)

	for _, want := range []string{
		"Context as the model sees it:",
		"Summary of the earlier part", // the label
		"EARLIER WORK",
		"Live tail:",
		"USER:",
		"did someone fix the poll loop?",
		"ASSISTANT:",
		"yes, done",
		"ok",
		"good",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("view missing %q:\n%s", want, got)
		}
	}
	// The system prompt is scaffolding, not history — it must not leak in.
	if strings.Contains(got, "# AGENTS.md") || strings.Contains(got, "# Editing rules") {
		t.Errorf("scaffolding leaked into the view:\n%s", got)
	}
}

// n caps the summaries shown; the live tail is not a summary, so it is not
// truncated by n — only the summaries are.
func TestViewContextCapsSummaries(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.HarnessNote(prompts.SummaryLabel + "ONE"),
		llm.HarnessNote(prompts.SummaryLabel + "TWO"),
		llm.HarnessNote(prompts.SummaryLabel + "THREE"),
		llm.TextMessage("user", "live tail"),
	}

	got := c.ViewContext(2)
	if strings.Contains(got, "THREE") {
		t.Errorf("third summary shown despite n=2:\n%s", got)
	}
	if !strings.Contains(got, "first 2 of 3 summaries shown") {
		t.Errorf("cap not announced:\n%s", got)
	}
	// The live tail is not a summary and is not capped away.
	if !strings.Contains(got, "live tail") {
		t.Errorf("live tail lost:\n%s", got)
	}
}

// No summary means there is nothing to fold: the whole history is the live tail,
// and the view says so plainly rather than pretending otherwise.
func TestViewContextNoSummary(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.TextMessage("user", "hello"),
		llm.TextMessage("assistant", "hi"),
	}

	got := c.ViewContext(-1)
	if !strings.Contains(got, "No compaction summaries") {
		t.Errorf("no-summary case not identified:\n%s", got)
	}
	for _, want := range []string{"USER:", "hello", "ASSISTANT:", "hi"} {
		if !strings.Contains(got, want) {
			t.Errorf("view missing %q:\n%s", want, got)
		}
	}
	// Scaffolding (the AGENTS.md editing rules) must not appear as history.
	if strings.Contains(got, "# Editing rules") {
		t.Errorf("system prompt leaked into the no-summary view:\n%s", got)
	}
}

// bare /context must not mutate the message slices it renders.
func TestViewContextDoesNotMutate(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.HarnessNote(prompts.SummaryLabel + "S"),
		llm.TextMessage("user", "u"),
	}
	c.curMessages = []llm.Message{llm.TextMessage("user", "c")}
	beforeDone := c.doneMessages
	beforeCur := c.curMessages

	_ = c.ViewContext(1)

	if len(c.doneMessages) != len(beforeDone) || len(c.curMessages) != len(beforeCur) {
		t.Fatal("message slices grew")
	}
	if c.doneMessages[0].Text() != beforeDone[0].Text() || c.curMessages[0].Text() != beforeCur[0].Text() {
		t.Error("message text changed")
	}
}

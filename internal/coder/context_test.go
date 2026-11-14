package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// A deterministic render of the fold the model reads: a roll's note standing
// for dropped turns, the settled history after it, and the turn in progress,
// in that order.
func TestViewContextRendersTheFold(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.HarnessNote(prompts.RollLabelStem + " EARLIER WORK"),
		llm.TextMessage("user", "did someone fix the poll loop?"),
		llm.TextMessage("assistant", "yes, done"),
	}
	c.curMessages = []llm.Message{
		llm.TextMessage("user", "ok"),
		llm.TextMessage("assistant", "good"),
	}

	got := c.ViewContext()

	for _, want := range []string{
		"Context as the model sees it:",
		"Earlier turns of this session were dropped", // the roll's label
		"EARLIER WORK",
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

// bare /context must not mutate the message slices it renders.
func TestViewContextDoesNotMutate(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.HarnessNote(prompts.RollLabelStem + " S"),
		llm.TextMessage("user", "u"),
	}
	c.curMessages = []llm.Message{llm.TextMessage("user", "c")}
	beforeDone := c.doneMessages
	beforeCur := c.curMessages

	_ = c.ViewContext()

	if len(c.doneMessages) != len(beforeDone) || len(c.curMessages) != len(beforeCur) {
		t.Fatal("message slices grew")
	}
	if c.doneMessages[0].Text() != beforeDone[0].Text() || c.curMessages[0].Text() != beforeCur[0].Text() {
		t.Error("message text changed")
	}
}

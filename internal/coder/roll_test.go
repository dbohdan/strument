package coder

import (
	"fmt"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// rollTurn is one turn: the user's request, a call, its result, an answer.
func rollTurn(n int) []llm.Message {
	const tokens = 300
	id := fmt.Sprintf("c%d", n)
	return []llm.Message{
		llm.TextMessage("user", fmt.Sprintf("Note for later: the code for turn %d is TURN-%03d. Read f%d.go.", n, n, n)),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", Arguments: `{"path":"f.go"}`}}},
		llm.ToolResult(id, strings.Repeat("x", tokens*4)),
		llm.TextMessage("assistant", fmt.Sprintf("Turn %d read it.", n)),
	}
}

func rollCoder(t *testing.T, mode string) (*Coder, *summaryStub) {
	t.Helper()
	c := testCoder(t)
	side := &summaryStub{}
	c.Summarizer = NewChatSummary(side, c.Model.SideModel, c.Tokens, c.Out, c.Clock, nil)
	c.CompactionMode = mode
	return c, side
}

// pairedCalls reports a tool result whose call is not in the history.
func unpairedResult(msgs []llm.Message) string {
	calls := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		if m.Role == llm.RoleTool && !calls[m.ToolCallID] {
			return m.ToolCallID
		}
	}
	return ""
}

func TestRollKeepsWhatTheUserSaid(t *testing.T) {
	c, side := rollCoder(t, CompactionRoll)
	for n := 1; n <= 8; n++ {
		c.doneMessages = append(c.doneMessages, rollTurn(n)...)
	}
	if !c.compactHistory(1200) {
		t.Fatal("nothing was rolled")
	}
	if side.calls != 0 {
		t.Errorf("a roll called the side model %d times", side.calls)
	}
	note := c.doneMessages[0].Text()
	if !strings.Contains(note, prompts.RollLabel) {
		t.Fatalf("the first message is not the roll's note: %q", note)
	}
	for n := 1; n <= 8; n++ {
		code := fmt.Sprintf("TURN-%03d", n)
		inNote := strings.Contains(note, code)
		inTail := false
		for _, m := range c.doneMessages[1:] {
			inTail = inTail || strings.Contains(m.Text(), code)
		}
		if !inNote && !inTail {
			t.Errorf("%s is neither in the note nor in the kept turns", code)
		}
		if inNote && inTail {
			t.Errorf("%s is both quoted and kept", code)
		}
	}
	if strings.Contains(note, "read it") {
		t.Error("the note carries the assistant's words")
	}
	if id := unpairedResult(c.doneMessages); id != "" {
		t.Errorf("result %s lost its call", id)
	}
	if c.Summarizer.total(c.doneMessages) > 1200 {
		t.Errorf("the rolled history is over budget: %d", c.Summarizer.total(c.doneMessages))
	}
}

// A second roll carries the first one's messages forward, in one note, in order.
func TestRollTwiceKeepsOneNote(t *testing.T) {
	c, _ := rollCoder(t, CompactionRoll)
	for n := 1; n <= 6; n++ {
		c.doneMessages = append(c.doneMessages, rollTurn(n)...)
	}
	c.compactHistory(1200)
	for n := 7; n <= 12; n++ {
		c.doneMessages = append(c.doneMessages, rollTurn(n)...)
	}
	if !c.compactHistory(1200) {
		t.Fatal("the second roll dropped nothing")
	}
	notes := 0
	for _, m := range c.doneMessages {
		if strings.Contains(m.Text(), prompts.RollLabelStem) {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("%d roll notes, want 1", notes)
	}
	note := c.doneMessages[0].Text()
	if i, j := strings.Index(note, "TURN-001"), strings.Index(note, "TURN-007"); i < 0 || j < 0 || i > j {
		t.Errorf("the note lost the first roll's messages or their order: %q", note)
	}
}

func TestRollBareKeepsNothing(t *testing.T) {
	c, _ := rollCoder(t, CompactionRollBare)
	for n := 1; n <= 8; n++ {
		c.doneMessages = append(c.doneMessages, rollTurn(n)...)
	}
	c.compactHistory(1200)
	note := c.doneMessages[0].Text()
	if !strings.Contains(note, prompts.RollBareLabel) || strings.Contains(note, "TURN-001") {
		t.Errorf("the bare roll's note: %q", note)
	}
}

// Over the cap — a quarter of the budget, never under minRollNoteCap — the
// oldest messages give way, and the note says how many.
func TestRollNoteCap(t *testing.T) {
	said := make([]string, 40)
	for i := range said {
		said[i] = fmt.Sprintf("message %02d %s", i, strings.Repeat("y", 4000))
	}
	note := rollNote(said, 2000, true).Text()
	if !strings.Contains(note, "message 39") {
		t.Error("the newest message was left out")
	}
	if strings.Contains(note, "message 00") {
		t.Error("the oldest message was kept although the note is over its cap")
	}
	if !strings.Contains(note, "did not fit") {
		t.Error("the note does not say that messages were left out")
	}
}

// The harness's own notes are not the user's words and are not quoted.
func TestRollQuotesOnlyTheUser(t *testing.T) {
	c, _ := rollCoder(t, CompactionRoll)
	c.doneMessages = append(c.doneMessages, llm.HarnessNote("The turns above are from a different model."))
	for n := 1; n <= 8; n++ {
		c.doneMessages = append(c.doneMessages, rollTurn(n)...)
	}
	c.compactHistory(1200)
	if strings.Contains(c.doneMessages[0].Text(), "different model") {
		t.Error("a harness note was quoted as something the user said")
	}
	c.ClearHistory()
	if len(c.rolledSaid) != 0 {
		t.Error("/clear kept the rolled messages")
	}
}

// A small budget does not shrink the cap below minRollNoteCap: a few dozen
// ordinary messages fit whole.
func TestRollNoteCapFloor(t *testing.T) {
	said := make([]string, 30)
	for i := range said {
		said[i] = fmt.Sprintf("message %02d %s", i, strings.Repeat("y", 300))
	}
	note := rollNote(said, 4000, true).Text()
	if !strings.Contains(note, "message 00") || strings.Contains(note, "did not fit") {
		t.Error("a small budget cut ordinary messages from the note")
	}
}

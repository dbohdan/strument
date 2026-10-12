package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// recallRecords is a three-turn session: the model reads a file in turn 1 and
// states a result; turn 2 searches once (its result must not be found again);
// turn 3 is in progress.
func recallRecords() []Record {
	return []Record{
		{Type: "session"},
		{Type: "message", Role: llm.RoleUser, Text: "Note for later: the codename is Marigold-417. Read big.go."},
		{Type: "message", Role: llm.RoleAssistant, ToolCalls: []RecordToolCall{{ID: "c1", Name: toolRead}}},
		{Type: "message", Role: llm.RoleTool, ToolCallID: "c1", Text: "1\tpackage big\n2\t// 878 lines follow\n3\tfunc Lines() {}"},
		{Type: "message", Role: llm.RoleAssistant, Text: "big.go is the longest, at 878 lines (the others are shorter)."},
		{Type: "turn"},
		{Type: "message", Role: llm.RoleUser, Text: "What was the count?"},
		{Type: "message", Role: llm.RoleAssistant, ToolCalls: []RecordToolCall{{ID: "c2", Name: toolRecall}}},
		{Type: "message", Role: llm.RoleTool, ToolCallID: "c2", Text: "t1.4 · turn 1 of 2 · assistant\n  1: big.go is the longest, at 878 lines."},
		{Type: "message", Role: llm.RoleAssistant, Text: "It was 878."},
		{Type: "turn"},
		{Type: "message", Role: llm.RoleUser, Text: "And the codename?"},
	}
}

func TestRecallEntriesNumberMessagesByTurn(t *testing.T) {
	entries, turns := recallEntries(recallRecords())
	if turns != 3 {
		t.Errorf("turns = %d, want 3", turns)
	}
	got := map[string]recallEntry{}
	for _, e := range entries {
		got[e.id()] = e
	}
	if e, ok := got["t1.3"]; !ok || e.role != llm.RoleTool || e.tool != toolRead {
		t.Errorf("t1.3 = %+v, want the read result, named", e)
	}
	if e := got["t1.4"]; !strings.Contains(e.text, "878 lines") {
		t.Errorf("t1.4 = %+v, want the assistant's answer", e)
	}
	// A previous recall's result is a copy of the record: finding it again
	// would count one statement twice.
	if _, ok := got["t2.3"]; ok {
		t.Error("an earlier recall result was searchable")
	}
	// A message with no text (a bare tool call) has nothing to find.
	if _, ok := got["t1.2"]; ok {
		t.Error("an empty message became an entry")
	}
	if e := got["t3.1"]; e.text != "And the codename?" {
		t.Errorf("t3.1 = %+v, want the turn in progress", e)
	}
}

func recallCoder(visible ...string) *Coder {
	c := &Coder{SessionRecords: recallRecords}
	for _, v := range visible {
		c.doneMessages = append(c.doneMessages, llm.TextMessage(llm.RoleUser, v))
	}
	return c
}

func TestRecallScopesToTheConversationAndSaysWhatItLeftOut(t *testing.T) {
	out := recallCoder().runRecall(recallArgs{Pattern: "878", From: "conversation", Newest: 10})
	if !strings.Contains(out, "t1.4") || !strings.Contains(out, "It was 878") {
		t.Errorf("the assistant's statements were not found:\n%s", out)
	}
	if strings.Contains(out, "t1.3") {
		t.Errorf("tool output was searched under the default scope:\n%s", out)
	}
	// The way out of the scope has to be in the result: the schema alone is
	// not where a model learns it needs it.
	if !strings.Contains(out, `1 more in tool output: pass from="all"`) {
		t.Errorf("the excluded match was not reported:\n%s", out)
	}
	// Newest first: turn 2's answer before turn 1's.
	if strings.Index(out, "t2.4") > strings.Index(out, "t1.4") {
		t.Errorf("results are not newest first:\n%s", out)
	}
}

func TestRecallCountsWhatIsStillInTheConversation(t *testing.T) {
	out := recallCoder("It was 878.").runRecall(recallArgs{Pattern: "878", From: "conversation", Newest: 10})
	if strings.Contains(out, "t2.4") {
		t.Errorf("a message still in the conversation was printed:\n%s", out)
	}
	if !strings.Contains(out, "1 more in messages still in this conversation") {
		t.Errorf("the visible match was not counted:\n%s", out)
	}
	if !strings.Contains(out, "t1.4") {
		t.Errorf("the removed match was lost:\n%s", out)
	}
}

func TestRecallNewestLimitsTheHits(t *testing.T) {
	out := recallCoder().runRecall(recallArgs{Pattern: "878", From: "all", Newest: 1})
	if !strings.Contains(out, "showing 1") || strings.Count(out, " · turn ") != 1 {
		t.Errorf("newest=1 showed more than one hit:\n%s", out)
	}
}

func TestRecallTreatsABrokenPatternAsText(t *testing.T) {
	out := recallCoder().runRecall(recallArgs{Pattern: "878 lines (", From: "all", Newest: 10})
	if !strings.Contains(out, "searched for as plain text") {
		t.Errorf("the fallback was not announced:\n%s", out)
	}
	if strings.Contains(out, "No matches") {
		t.Errorf("the literal search found nothing:\n%s", out)
	}
}

func TestRecallOpensAnEntryWhole(t *testing.T) {
	out := recallCoder().runRecall(recallArgs{Pattern: "878", ID: "t1.3"})
	for _, want := range []string{"  1: 1\tpackage big", "> 2: 2\t// 878 lines follow", "  3: 3\tfunc Lines() {}"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if out := recallCoder().runRecall(recallArgs{Pattern: "x", ID: "t9.9"}); !strings.Contains(out, "No message has the id") {
		t.Errorf("an unknown id was not reported:\n%s", out)
	}
}

func TestRecallArgsDefaultsAndBounds(t *testing.T) {
	a, msg := parseRecallArgs(llm.ToolCall{Arguments: `{"pattern":"x","newest":500}`})
	if msg != "" || a.From != "conversation" || a.Newest != recallMaxNewest {
		t.Errorf("got %+v %q", a, msg)
	}
	if _, msg := parseRecallArgs(llm.ToolCall{Arguments: `{"pattern":"x","from":"files"}`}); msg == "" {
		t.Error("an unknown scope was accepted")
	}
	if _, msg := parseRecallArgs(llm.ToolCall{Arguments: `{}`}); msg == "" {
		t.Error("a missing pattern was accepted")
	}
}

func TestRecallIsOfferedOnlyWithARecord(t *testing.T) {
	offered := func(c *Coder) bool {
		for _, d := range c.toolDefs() {
			if d.Name == toolRecall {
				return true
			}
		}
		return false
	}
	c := &Coder{OfferRecall: true}
	if offered(c) {
		t.Error("offered with no record to search")
	}
	c.SessionRecords = recallRecords
	if !offered(c) {
		t.Error("not offered with a record and the flag")
	}
	c.OfferRecall = false
	if offered(c) {
		t.Error("offered without the flag")
	}
}

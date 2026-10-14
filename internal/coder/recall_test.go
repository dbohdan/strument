package coder

import (
	"regexp"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
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

// Before the turn in progress has a message in the record, it still counts:
// two searches in one turn must agree on the number of turns.
func TestRecallCountsTheTurnInProgress(t *testing.T) {
	recs := recallRecords()
	recs = append(recs[:len(recs)-1], Record{Type: "message", Role: llm.RoleAssistant, Text: "x"}, Record{Type: "turn"})
	if _, turns := recallEntries(recs); turns != 4 {
		t.Errorf("turns = %d, want 4: three finished and one in progress", turns)
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
	// An id alone opens the message, unmarked: a live session was refused for
	// leaving out the pattern and had to invent one.
	if out := recallCoder().runRecall(recallArgs{ID: "t1.3"}); !strings.Contains(out, "  2: 2\t// 878 lines follow") {
		t.Errorf("an id alone did not open the message:\n%s", out)
	}
	if _, msg := parseRecallArgs(llm.ToolCall{Arguments: `{"id":"t1.3"}`}); msg != "" {
		t.Errorf("an id alone was refused: %s", msg)
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
		t.Error("a call with neither pattern nor id was accepted")
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
	c := &Coder{}
	if offered(c) {
		t.Error("offered with no record to search")
	}
	c.SessionRecords = recallRecords
	if !offered(c) {
		t.Error("not offered with a record")
	}
}

// Inside run_code, recall returns lines as data, so a program can post-process
// a search; matches still in the conversation are included and marked.
func TestRecallInsideAProgramReturnsData(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.SessionRecords = recallRecords
	c.doneMessages = []llm.Message{llm.TextMessage(llm.RoleAssistant, "It was 878.")}
	got := run(c, `const hits = recall({pattern: "878", from: "all"});
hits.map(h => [h.id, h.speaker, h.in_conversation, h.line].join(" ")).join("; ")`)
	for _, want := range []string{"t2.4 assistant true 1", "t1.4 assistant false 1", "t1.3 tool read false 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// Positional, like grep's pattern-first call.
	if got := run(c, `recall("Marigold").length`); !strings.Contains(got, "1") {
		t.Errorf("positional call: %s", got)
	}
	if !strings.Contains(codeTool(c.codeCallableTools(), true, c.dataFuncs()).Description, "recall({pattern") {
		t.Error("the run_code description does not list recall when it is offered")
	}
}

// A session without a record has no recall in programs either: a program
// can reach exactly the tools the model can.
func TestRecallIsNotInProgramsWhenNotOffered(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.SessionRecords = nil
	if got := run(c, `recall("878")`); !strings.Contains(got, "ReferenceError") {
		t.Errorf("recall was callable without being offered:\n%s", got)
	}
	if strings.Contains(codeTool(c.codeCallableTools(), true, c.dataFuncs()).Description, "recall") {
		t.Error("the run_code description lists recall when it is not offered")
	}
}

// With recall offered, the roll note no longer says the dropped work is gone:
// it says where it is, once before the quotes and in full after them. Without
// recall it is byte for byte what it was.
func TestRollNoteWithRecallPointsAtTheRecord(t *testing.T) {
	said := []string{"Note for later: the codename is Marigold-417.", "Read big.go."}
	with := rollNote(said, 4000, true, true).Text()
	if !strings.Contains(with, prompts.RollLabelStem) {
		t.Errorf("the stem is gone:\n%s", with)
	}
	if strings.Contains(with, "is not kept") {
		t.Errorf("the note says the work is gone while recall can find it:\n%s", with)
	}
	quote, tail := strings.LastIndex(with, "> Read big.go."), strings.Index(with, prompts.RollRecallTail)
	if quote < 0 || tail < quote {
		t.Errorf("the instruction does not follow the quotes:\n%s", with)
	}
	without := rollNote(said, 4000, true, false).Text()
	if !strings.Contains(without, prompts.RollLabel) || strings.Contains(without, "recall") {
		t.Errorf("the note without recall changed:\n%s", without)
	}
}

// The placeholder names recall only when it is offered, and a result cleared
// under either placeholder is recognized as cleared, not cleared again.
func TestClearedPlaceholderFollowsRecall(t *testing.T) {
	c := &Coder{}
	if c.clearedPlaceholder() != clearedToolResult {
		t.Error("the placeholder names recall without it")
	}
	c.SessionRecords = recallRecords
	if p := c.clearedPlaceholder(); p != clearedToolResultRecall || !strings.Contains(p, `from="all"`) {
		t.Errorf("placeholder with recall = %q", p)
	}
	count := func(m llm.Message) int { return len(m.Text()) / 4 }
	big := strings.Repeat("x", 8000)
	msgs := []llm.Message{
		{Role: llm.RoleTool, Content: llm.TextContent(clearedToolResultRecall)},
		{Role: llm.RoleTool, Content: llm.TextContent(big)},
		{Role: llm.RoleUser, Content: llm.TextContent(strings.Repeat("y", 400))},
	}
	n, _ := pruneToolResults(msgs, 1000, count, clearedToolResult)
	if n != 1 || msgs[0].Text() != clearedToolResultRecall {
		t.Errorf("cleared %d; a result already cleared under the other placeholder was rewritten: %q", n, msgs[0].Text())
	}
}

// An id passed as the pattern, with or without the dot escaped, opens the
// message: as a pattern it could only ever come back empty. A pattern that
// merely starts like an id is still a search.
func TestRecallOpensAnIDPassedAsPattern(t *testing.T) {
	c := &Coder{SessionRecords: recallRecords}
	for _, p := range []string{`t1.4`, `t1\.4`} {
		got := c.runRecall(recallArgs{Pattern: p, From: "all", Newest: 10})
		if !strings.Contains(got, `opened as id="t1.4"`) || !strings.Contains(got, "878 lines") {
			t.Errorf("pattern %q:\n%s", p, got)
		}
	}
	if got := c.runRecall(recallArgs{Pattern: `t1\.[0-9]`, From: "all", Newest: 10}); strings.Contains(got, "opened as id") {
		t.Errorf("a range was taken for an id:\n%s", got)
	}
}

// When the cutoff hides replies, the header names them by id, so a model
// that does not raise newest can still reach the answer it gave. Tool output
// hidden by the cutoff is not named; only replies are.
func TestRecallNamesRepliesTheCutoffHid(t *testing.T) {
	entries := []recallEntry{
		{turn: 1, index: 4, role: llm.RoleAssistant, text: "big.go is the longest, at 878 lines."},
		{turn: 2, index: 3, role: llm.RoleTool, tool: toolRead, text: "the longest wait"},
		{turn: 3, index: 3, role: llm.RoleTool, tool: toolRead, text: "the longest name"},
		{turn: 4, index: 3, role: llm.RoleTool, tool: toolRead, text: "the longest line"},
	}
	re := regexp.MustCompile("longest")
	got := recallSearch(entries, 5, re, recallArgs{From: "all", Newest: 2}, nil)
	if !strings.Contains(got, "Not shown, your own replies: t1.4;") {
		t.Errorf("the hidden reply is not named:\n%s", got)
	}
	if strings.Contains(got, "t2.3;") || strings.Contains(got, "t2.3,") {
		t.Errorf("hidden tool output was named as a reply:\n%s", got)
	}
	if got := recallSearch(entries, 5, re, recallArgs{From: "all", Newest: 10}, nil); strings.Contains(got, "Not shown") {
		t.Errorf("named hidden replies when nothing was hidden:\n%s", got)
	}
}

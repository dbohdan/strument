package coder

import (
	"fmt"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// /tokens claims to say what the next request will carry. The claim is only
// worth making if the number tracks what actually goes out, so this compares
// the report's total against a count over buildRequest's own output — the same
// messages plus the same tool schemas.
//
// Before the tool-schema row existed, a fresh session reported ~1.1k while
// sending ~3k, and the missing two thirds were the schemas plus the tool calls
// in history. A reader watching that figure approach a declared context window
// would have been reassured by a number that was wrong by a factor of three.
func TestTokensReportTracksTheRequest(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.curMessages = []llm.Message{
		llm.TextMessage("user", strings.Repeat("q", 400)),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "edit", Arguments: `{"path":"a.go","old_string":"` +
				strings.Repeat("x", 2000) + `"}`},
		}},
		llm.ToolResult("c1", "Applied the edit to a.go"),
	}

	req := c.buildRequest(c.formatMessages().allMessages())
	actual := c.countMessages(req.Messages) + c.countTools()

	report := c.TokensReport()
	var reported int
	for line := range strings.SplitSeq(report, "\n") {
		if strings.Contains(line, "total") {
			if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &reported); err != nil {
				t.Fatalf("cannot parse total from %q", line)
			}
			break
		}
	}
	if reported == 0 {
		t.Fatalf("no total in:\n%s", report)
	}
	// Within 2%: the two paths count the same bytes, so any real gap is a
	// section the report forgot.
	if diff := reported - actual; diff > actual/50 || diff < -actual/50 {
		t.Errorf("report says %d, request carries %d (%+d)\n%s", reported, actual, diff, report)
	}
	if !strings.Contains(report, "tool schemas") {
		t.Error("the schemas ride on every request and must have a row")
	}
}

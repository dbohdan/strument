package coder

import (
	"fmt"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// The history budget and the helpers the compaction tests share.

// summaryOutput collects what Strument prints, for tests that read it.
type summaryOutput struct{ lines []string }

func (o *summaryOutput) Printf(format string, args ...any) {
	o.lines = append(o.lines, fmt.Sprintf(format, args...))
}
func (o *summaryOutput) Warningf(format string, args ...any) { o.Printf(format, args...) }
func (o *summaryOutput) CommandOutput(text string)           { o.Printf("%s", text) }
func (o *summaryOutput) Errorf(format string, args ...any)   { o.Printf(format, args...) }
func (o *summaryOutput) Toolf(format string, args ...any)    { o.Printf(format, args...) }
func (o *summaryOutput) ToolBlock(title, body string)        { o.Printf("%s %s", title, body) }
func (*summaryOutput) Link(string)                           {}
func (*summaryOutput) StreamText(string)                     {}
func (*summaryOutput) StreamReasoning(string)                {}
func (*summaryOutput) StreamToolCall(int, string, string)    {}
func (*summaryOutput) FlushStream()                          {}

// msgTok builds a message of exactly 400 tokens under RuneCounter (runes/4).
func msgTok(role string) llm.Message {
	const tokens = 400
	return llm.TextMessage(role, strings.Repeat("x", tokens*4))
}

func TestMaxChatHistoryTokens(t *testing.T) {
	for _, c := range []struct{ ctx, want int }{
		{0, 2048},           // unknown => floor
		{3000, 2048},        // 1500 -> clamped up
		{4096, 2048},        // exactly the floor
		{100000, 50000},     // within range
		{200000, 100000},    // within range
		{1_000_000, 500000}, // within range
	} {
		if got := maxChatHistoryTokens(c.ctx); got != c.want {
			t.Errorf("maxChatHistoryTokens(%d) = %d, want %d", c.ctx, got, c.want)
		}
	}
}

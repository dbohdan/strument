package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// The history budget and the helpers the compaction tests share.

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

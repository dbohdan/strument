package coder

import (
	"context"
	"iter"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// asideStub answers with a fixed string and no usage event, so finalizeUsage
// falls back to its own estimate.
type asideStub struct{}

func (asideStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "42"}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func TestRunAsideIsolatedFromContext(t *testing.T) {
	c := testCoder(t)
	c.Client = asideStub{}
	c.curMessages = []llm.Message{llm.TextMessage("user", "prior turn")}
	c.doneMessages = []llm.Message{llm.TextMessage("user", "older"), llm.TextMessage("assistant", "reply")}
	sentBefore, _ := c.SessionTokens()

	ans := c.RunAside(context.Background(), "what is 6 times 7?")

	if ans != "42" {
		t.Errorf("answer = %q, want 42", ans)
	}
	// The one-off must not become part of the conversation.
	if len(c.curMessages) != 1 {
		t.Errorf("RunAside changed curMessages: len %d, want 1", len(c.curMessages))
	}
	if len(c.doneMessages) != 2 {
		t.Errorf("RunAside changed doneMessages: len %d, want 2", len(c.doneMessages))
	}
	// But usage is still reported: the session totals advance.
	if sentAfter, _ := c.SessionTokens(); sentAfter <= sentBefore {
		t.Errorf("usage not reported: session tokens %d -> %d", sentBefore, sentAfter)
	}
}

package coder

import (
	"context"
	"iter"
	"math"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func TestCalibrateTokens(t *testing.T) {
	c := testCoder(t)
	c.calibrateTokens(100, 140)
	if math.Abs(c.tokenScale-1.4) > 1e-9 {
		t.Fatalf("first response: scale %v, want 1.4", c.tokenScale)
	}
	c.calibrateTokens(100, 100)
	if math.Abs(c.tokenScale-1.2) > 1e-9 {
		t.Errorf("second response: scale %v, want the smoothed 1.2", c.tokenScale)
	}
	c.calibrateTokens(100, 100000)
	if c.tokenScale > 3 {
		t.Errorf("a nonsense count moved the scale past its clamp: %v", c.tokenScale)
	}
	c.calibrateTokens(0, 50)
	c.calibrateTokens(50, 0)
	c.SetModel(c.Model)
	if c.tokenScale != 0 {
		t.Errorf("a model switch kept the old model's scale: %v", c.tokenScale)
	}
}

// countingAnswer answers and reports a prompt `scale` times Strument's estimate,
// as a provider whose tokenizer is denser than characters over four would.
type countingAnswer struct {
	c     *Coder
	scale float64
}

func (a *countingAnswer) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	prompt := int(float64(a.c.requestTokens(req.Messages)) * a.scale)
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: &llm.Usage{PromptTokens: prompt, CompletionTokens: 2}}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// The Larkspur case: a history under budget by the estimate and over it by the
// provider's count folds once the provider has said so, at the end of the turn.
func TestCalibratedBudgetFolds(t *testing.T) {
	c := testCoder(t)
	side := &summaryStub{}
	c.Summarizer = NewChatSummary(side, c.Model.SideModel, c.Tokens, c.Out, c.Clock, nil)
	for range 6 {
		c.doneMessages = append(c.doneMessages, msgTok("user", 400), msgTok("assistant", 400))
	}
	est := c.Summarizer.total(c.doneMessages)
	c.Model.Context = 2*est + 2000 // the budget, half the window, sits just above the estimate
	c.maybeSummarize()
	if side.calls != 0 {
		t.Fatal("folded before any provider count, on the estimate alone")
	}

	c.Client = &countingAnswer{c: c, scale: 1.4}
	c.runOne(context.Background(), "carry on")

	if c.tokenScale < 1.3 {
		t.Fatalf("the provider's count was not learned: scale %v", c.tokenScale)
	}
	if side.calls == 0 && !hasSummaryContent(c.doneMessages) {
		t.Error("a history over budget by the provider's count did not fold at turn end")
	}
}

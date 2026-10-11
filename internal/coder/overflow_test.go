package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// refusingClient refuses its first `refuse` requests as over the context limit,
// then answers, and counts the requests it saw.
type refusingClient struct{ refuse, sends int }

func (r *refusingClient) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	r.sends++
	refused := r.sends <= r.refuse
	return func(yield func(llm.StreamEvent, error) bool) {
		if refused {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrContextWindow, Message: "prompt is too long"})
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// promptRecorder says yes and keeps the prompts it was shown.
type promptRecorder struct{ prompts []string }

func (c *promptRecorder) Confirm(r ConfirmRequest) ConfirmResult {
	c.prompts = append(c.prompts, r.Prompt)
	return ConfirmResult{Yes: true}
}

func overflowCoder(t *testing.T, client llm.ModelClient) (*Coder, *summaryStub) {
	t.Helper()
	c := testCoder(t)
	c.Client = client
	side := &summaryStub{}
	c.Summarizer = NewChatSummary(side, c.Model.SideModel, c.Tokens, c.Out, c.Clock, nil)
	for range 6 {
		c.doneMessages = append(c.doneMessages, msgTok("user", 400), msgTok("assistant", 400))
	}
	return c, side
}

// A provider's "too long" folds the settled history and the request goes
// again, once; the turn succeeds instead of ending on the refusal.
func TestOverflowFoldsAndRetries(t *testing.T) {
	client := &refusingClient{refuse: 1}
	c, side := overflowCoder(t, client)
	before := len(c.doneMessages)

	outcome, _ := c.sendMessage(context.Background(), "carry on")

	if client.sends != 2 {
		t.Fatalf("the main model saw %d requests, want the refused one and one retry", client.sends)
	}
	if side.calls == 0 || len(c.doneMessages) >= before || !hasSummaryContent(c.doneMessages) {
		t.Errorf("the history was not folded: %d -> %d messages, %d summary calls",
			before, len(c.doneMessages), side.calls)
	}
	if outcome == OutcomeContextExhausted {
		t.Error("the turn still ended on the refusal")
	}
}

// Still too long after one fold means this turn is too long, which no fold
// can help: the turn ends as before, without a third request.
func TestOverflowRetriesOnlyOnce(t *testing.T) {
	client := &refusingClient{refuse: 99}
	c, _ := overflowCoder(t, client)

	outcome, _ := c.sendMessage(context.Background(), "carry on")

	if client.sends != 2 {
		t.Errorf("the main model saw %d requests, want 2", client.sends)
	}
	if outcome != OutcomeContextExhausted {
		t.Errorf("outcome = %v, want context exhausted", outcome)
	}
}

// Over the limit by Strument's own count: the history is folded before
// anything is sent, and the user is not asked whether to send it anyway.
func TestOverLimitFoldsBeforeAsking(t *testing.T) {
	client := &refusingClient{}
	c, side := overflowCoder(t, client)
	conf := &promptRecorder{}
	c.Confirm = conf
	// The window holds the turn and a summary, not the whole history.
	c.Model.Context = c.requestTokens(projectForModel(c.formatMessages().allMessages(), c.Model)) - 1000

	c.sendMessage(context.Background(), "carry on")

	if side.calls == 0 {
		t.Fatal("the history was not folded before sending")
	}
	for _, p := range conf.prompts {
		if strings.Contains(p, "anyway") {
			t.Errorf("the user was asked %q although a fold made room", p)
		}
	}
	if client.sends != 1 {
		t.Errorf("the main model saw %d requests, want 1", client.sends)
	}
}

// No summarizer or no settled history: nothing to fold, and the refusal ends
// the turn as it always did.
func TestOverflowWithNothingToFold(t *testing.T) {
	client := &refusingClient{refuse: 1}
	c := testCoder(t)
	c.Client = client

	outcome, _ := c.sendMessage(context.Background(), "carry on")

	if client.sends != 1 || outcome != OutcomeContextExhausted {
		t.Errorf("sends = %d, outcome = %v; want 1 and context exhausted", client.sends, outcome)
	}
}

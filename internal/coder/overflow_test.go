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

func overflowCoder(t *testing.T, client llm.ModelClient) *Coder {
	t.Helper()
	c := testCoder(t)
	c.Client = client
	for range 6 {
		c.doneMessages = append(c.doneMessages, msgTok("user"), msgTok("assistant"))
	}
	return c
}

// A provider's "too long" folds the settled history and the request goes
// again, once; the turn succeeds instead of ending on the refusal.
func TestOverflowFoldsAndRetries(t *testing.T) {
	client := &refusingClient{refuse: 1}
	c := overflowCoder(t, client)
	before := len(c.doneMessages)

	outcome, _ := c.sendMessage(context.Background(), "carry on")

	if client.sends != 2 {
		t.Fatalf("the main model saw %d requests, want the refused one and one retry", client.sends)
	}
	if len(c.doneMessages) >= before || !rolled(c) {
		t.Errorf("the history was not folded: %d -> %d messages", before, len(c.doneMessages))
	}
	if outcome == OutcomeContextExhausted {
		t.Error("the turn still ended on the refusal")
	}
}

// Still too long after one fold means this turn is too long, which no fold
// can help: the turn ends as before, without a third request.
func TestOverflowRetriesOnlyOnce(t *testing.T) {
	client := &refusingClient{refuse: 99}
	c := overflowCoder(t, client)

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
	c := overflowCoder(t, client)
	conf := &promptRecorder{}
	c.Confirm = conf
	// The window holds the turn and a folded history, not the whole of it.
	c.Model.Context = c.requestTokens(projectForModel(c.formatMessages().allMessages(), c.Model)) - 1000

	c.sendMessage(context.Background(), "carry on")

	if !rolled(c) {
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

// No settled history: nothing to fold, and the refusal ends
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

// statingClient refuses once as llama.cpp does, giving its context size.
type statingClient struct {
	limit, sends int
}

func (r *statingClient) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	r.sends++
	first := r.sends == 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if first {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrContextWindow, ContextLimit: r.limit,
				Message: "HTTP 400: request exceeds the available context size"})
			return
		}
		if yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
		}
	}
}

// A server started with a smaller context than the config says: its number
// is used for the rest of the session, the user is told which setting to
// change, and the fold that follows rescues the turn.
func TestServerContextIsAdopted(t *testing.T) {
	client := &statingClient{limit: 65536}
	c := overflowCoder(t, client)
	c.Model.Alias = "local"
	c.Model.Context = 131072
	out := &captureOut{}
	c.Out = out

	outcome, _ := c.sendMessage(context.Background(), "carry on")

	if c.Model.Context != 65536 {
		t.Errorf("context = %d, want the server's 65536", c.Model.Context)
	}
	want := `The server's context is 65536 tokens, smaller than the 131072 configured for "local".`
	if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "set context = 65536") {
		t.Errorf("output = %q, want the warning naming both numbers and the setting", out.String())
	}
	if outcome == OutcomeContextExhausted || client.sends != 2 {
		t.Errorf("outcome = %v after %d requests, want the fold to rescue the turn", outcome, client.sends)
	}
}

// A stated limit at or above the configured one changes nothing: the config
// is already the smaller number, and the refusal is about this request.
func TestLargerServerContextIsIgnored(t *testing.T) {
	c := overflowCoder(t, &statingClient{limit: 200000})
	c.Model.Context = 131072
	out := &captureOut{}
	c.Out = out

	c.sendMessage(context.Background(), "carry on")

	if c.Model.Context != 131072 {
		t.Errorf("context = %d, want the configured 131072 kept", c.Model.Context)
	}
	if strings.Contains(out.String(), "The server's context") {
		t.Errorf("warned about a larger server context: %q", out.String())
	}
}

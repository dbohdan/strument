package coder

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// The weak model fails more often than the main one, and its calls go out at
// the moments nobody is watching (after the edits, before the prompt returns).
// A retryable blip used to cost the commit its message outright; these pin
// that the side calls ride the same backoff a turn does.

func TestCommitMessengerRetriesTransientError(t *testing.T) {
	stub := &retryOnceStub{}
	clock := &fastClock{}
	out := &summaryOutput{}
	msg := CommitMessenger(stub, &config.Model{Slug: "weak"}, "", nil, out, clock)

	got := msg("", "diff text")

	if got != "42" {
		t.Errorf("message = %q, want 42 after one retry", got)
	}
	if len(clock.slept) != 1 {
		t.Errorf("expected exactly one backoff sleep, got %v", clock.slept)
	}
	if !strings.Contains(strings.Join(out.lines, "\n"), "Retrying in") {
		t.Error("the retry was not reported to the user")
	}
}

func TestCommitMessengerGivesUpAfterNonRetryableError(t *testing.T) {
	clock := &fastClock{}
	msg := CommitMessenger(nonRetryableStub{}, &config.Model{Slug: "weak"}, "", nil, &summaryOutput{}, clock)

	if got := msg("", "diff text"); got != "" {
		t.Errorf("message = %q, want empty so the caller falls back", got)
	}
	if len(clock.slept) != 0 {
		t.Errorf("a non-retryable error still slept: %v", clock.slept)
	}
}

func TestNotesWriterRetriesTransientError(t *testing.T) {
	stub := &retryOnceStub{}
	clock := &fastClock{}
	write := NotesWriter(stub, &config.Model{Slug: "weak"}, nil, &summaryOutput{}, clock)

	if got := write("## a turn"); got != "42" {
		t.Errorf("notes = %q, want 42 after one retry", got)
	}
	if len(clock.slept) != 1 {
		t.Errorf("expected exactly one backoff sleep, got %v", clock.slept)
	}
}

func TestChatSummaryRetriesTransientError(t *testing.T) {
	stub := &retryOnceStub{}
	clock := &fastClock{}
	s := NewChatSummary(stub, &config.Model{Slug: "weak", Context: 100000}, RuneCounter{}, &summaryOutput{}, clock)
	msgs := []llm.Message{msgTok("user", 80), msgTok("assistant", 80)}

	out, err := s.summarizeAll(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out[0].Text(), prompts.SummaryLabel+"42") {
		t.Errorf("summary = %q, want the answer that arrived after the retry", out[0].Text())
	}
	if len(clock.slept) != 1 {
		t.Errorf("expected exactly one backoff sleep, got %v", clock.slept)
	}
}

// nonRetryableStub fails with an auth error, which the retry table refuses.
type nonRetryableStub struct{}

func (nonRetryableStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrAuth, Message: "bad key"})
	}
}

// A permanently failing weak model exhausts the backoff and fails rather than
// looping: the delay doubles to the cap and the next failure ends the call.
func TestSendSideStopsAtTheCap(t *testing.T) {
	clock := &fastClock{}
	out := &summaryOutput{}
	start := time.Now()

	ans, err := sendSide(context.Background(), summaryErrStub{}, llm.Request{}, out, clock, nil)

	if err == nil || ans != "" {
		t.Errorf("expected failure, got %q / %v", ans, err)
	}
	if len(clock.slept) == 0 {
		t.Error("no backoff sleeps happened before giving up")
	}
	if last := clock.slept[len(clock.slept)-1]; last > retryTimeout {
		t.Errorf("a sleep of %s exceeded the %s cap", last, retryTimeout)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("test took %s; the fast clock is not being used", elapsed)
	}
}

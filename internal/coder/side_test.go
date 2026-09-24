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

// The side model fails more often than the main one, and its calls go out at
// the moments nobody is watching (after the edits, before the prompt returns).
// A retryable blip used to cost the commit its message outright; these pin
// that the side calls ride the same backoff a turn does.

func TestCommitMessengerRetriesTransientError(t *testing.T) {
	stub := &retryOnceStub{}
	clock := &fastClock{}
	out := &summaryOutput{}
	msg := CommitMessenger(stub, &config.Model{Slug: "side"}, "", nil, out, clock, "", nil)

	got := msg("", "diff text")

	if got != "42" {
		t.Errorf("message = %q, want 42 after one retry", got)
	}
	if len(clock.slept) != 1 {
		t.Errorf("expected exactly one backoff sleep, got %v", clock.slept)
	}
	// Reported, and named. A countdown during a wait for your own prompt says
	// nothing about which side call is retrying unless it says so.
	said := strings.Join(out.lines, "\n")
	if !strings.Contains(said, "Retrying commit message in") {
		t.Errorf("the retry was not reported as the commit message's:\n%s", said)
	}
}

func TestCommitMessengerGivesUpAfterNonRetryableError(t *testing.T) {
	clock := &fastClock{}
	msg := CommitMessenger(nonRetryableStub{}, &config.Model{Slug: "side"}, "", nil, &summaryOutput{}, clock, "", nil)

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
	write := NotesWriter(stub, &config.Model{Slug: "side"}, nil, &summaryOutput{}, clock, nil)

	got, err := write("## a turn")
	if err != nil {
		t.Fatal(err)
	}
	if got != "42" {
		t.Errorf("notes = %q, want 42 after one retry", got)
	}
	if len(clock.slept) != 1 {
		t.Errorf("expected exactly one backoff sleep, got %v", clock.slept)
	}
}

func TestChatSummaryRetriesTransientError(t *testing.T) {
	stub := &retryOnceStub{}
	clock := &fastClock{}
	s := NewChatSummary(stub, &config.Model{Slug: "side", Context: 100000}, RuneCounter{}, &summaryOutput{}, clock, nil)
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

// A permanently failing side model exhausts the backoff and fails rather than
// looping: the delay doubles to the cap and the next failure ends the call.
func TestSendSideStopsAtTheCap(t *testing.T) {
	clock := &fastClock{}
	out := &summaryOutput{}
	start := time.Now()

	ans, err := sendSide(context.Background(), summaryErrStub{}, llm.Request{}, "a side call", out, clock, nil, nil)

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

// emptyThenStub answers blank a fixed number of times, then answers properly.
// It counts calls, because the point of the empty-response change is that a
// second request goes out at all.
type emptyThenStub struct {
	blanks int
	calls  int
}

func (s *emptyThenStub) Send(context.Context, llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		s.calls++
		if s.calls <= s.blanks {
			// A 200 that streams no content: no error, and nothing but
			// whitespace where the answer should be.
			if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "   \n"}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "fix(poll): raise the interval"}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// TestSendSideRetriesAnEmptyResponse pins the classification change. A 200 with
// no content used to return as a real answer, so no retry went out and the
// commit landed with "(no commit message provided)" — a phrase that reads as a
// decision rather than a failure. Live, that was 21 of roughly 250 commits.
func TestSendSideRetriesAnEmptyResponse(t *testing.T) {
	stub := &emptyThenStub{blanks: 1}
	clock := &fastClock{}
	out := &summaryOutput{}

	got, err := sendSide(context.Background(), stub, llm.Request{}, "a side call", out, clock, nil, nil)

	if err != nil || got != "fix(poll): raise the interval" {
		t.Errorf("got %q / %v, want the answer from the second attempt", got, err)
	}
	if stub.calls != 2 {
		t.Errorf("%d requests went out, want 2 — the blank one was not retried", stub.calls)
	}
	said := strings.Join(out.lines, "\n")
	if !strings.Contains(said, "Retrying a side call in") {
		t.Errorf("the retry was silent or unnamed:\n%s", said)
	}
}

// TestSendSideStopsRetryingEmptyResponses bounds it. A provider answering
// 200-with-nothing repeatedly is not warming up, and every further attempt is
// another paid request for the same nothing — so the empty budget is far below
// the nine attempts a transient error gets.
func TestSendSideStopsRetryingEmptyResponses(t *testing.T) {
	stub := &emptyThenStub{blanks: 99}
	clock := &fastClock{}
	out := &summaryOutput{}

	got, err := sendSide(context.Background(), stub, llm.Request{}, "a side call", out, clock, nil, nil)

	if err == nil || got != "" {
		t.Errorf("got %q / %v, want a failure so the caller falls back", got, err)
	}
	if stub.calls != maxEmptyRetries+1 {
		t.Errorf("%d requests went out, want %d", stub.calls, maxEmptyRetries+1)
	}
	// The caller falls back either way; what changed is that the user is told
	// which of the two happened.
	if !strings.Contains(strings.Join(out.lines, "\n"), emptySideResponse) {
		t.Errorf("the failure was not reported:\n%s", strings.Join(out.lines, "\n"))
	}
}

// TestCommitMessengerFallsBackOnEmptyResponse walks the whole path a commit
// takes, since that is where this was found.
func TestCommitMessengerFallsBackOnEmptyResponse(t *testing.T) {
	stub := &emptyThenStub{blanks: 99}
	msg := CommitMessenger(stub, &config.Model{Slug: "side"}, "", nil, &summaryOutput{}, &fastClock{}, "", nil)

	if got := msg("", "diff text"); got != "" {
		t.Errorf("message = %q, want empty so gitrepo falls back", got)
	}
	if stub.calls != maxEmptyRetries+1 {
		t.Errorf("%d requests went out, want %d", stub.calls, maxEmptyRetries+1)
	}
}

// The retry ladder has to fit inside the budget it runs under.
//
// This is the bug that made session notes look unreliable, and it was pure
// arithmetic: every side call had 60s for the whole attempt, while the ladder
// it ran was the turn's, capped at retryTimeout — 0.25+0.5+1+2+4+8+16+32 =
// 63.75s of sleeping alone, before the failed requests themselves. The ladder
// could not finish, so the retries it appeared to promise were never going to
// happen, and the call died of its own deadline partway through.
//
// Nothing in the code connected the two numbers, so nothing objected. This is
// that connection. It fails if either constant moves out from under the other.
func TestSideLadderFitsItsBudget(t *testing.T) {
	var total time.Duration
	rb := retryBackoff{delay: initialRetryDelay, cap: sideRetryCap}
	clock := &fastClock{}
	out := &summaryOutput{}
	transient := &llm.StreamError{Class: llm.ErrServer, Message: "busy"}

	for rb.retry(context.Background(), out, clock, transient) {
		if len(clock.slept) > 50 {
			t.Fatal("the ladder does not terminate")
		}
	}
	for _, d := range clock.slept {
		total += d
	}
	if total >= sideTimeout {
		t.Errorf("the ladder sleeps %v inside a %v budget, so it can never run to the end "+
			"— raise sideTimeout or lower sideRetryCap", total, sideTimeout)
	}
	// And it must still be a ladder rather than a single attempt: giving up
	// instantly would satisfy the line above and help nobody.
	if len(clock.slept) < 4 {
		t.Errorf("only %d retries fit; that is not worth calling a backoff", len(clock.slept))
	}
	t.Logf("side ladder: %d retries, %v total, inside a %v budget", len(clock.slept), total, sideTimeout)
}

// A side call killed by its own deadline says so, instead of returning an empty
// answer that reads as a model with nothing to say.
//
// The old code short-circuited on ctx.Err() *before* the one function that
// prints, so this path was mute — and `/notes generate` reported it as "the
// model returned no notes", which is a budget we imposed wearing the costume of
// a decision the model made.
func TestSideCallReportsItsOwnDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already past its deadline when the first attempt fails

	out := &summaryOutput{}
	got, err := sendSide(ctx, &emptyThenStub{blanks: 99}, llm.Request{}, "session notes",
		out, &fastClock{}, nil, nil)

	if got != "" {
		t.Errorf("got %q, want nothing", got)
	}
	if err == nil {
		t.Fatal("a call that ran out of time returned no error")
	}
	said := strings.Join(out.lines, "\n")
	if !strings.Contains(said, "session notes") {
		t.Errorf("the failure did not name the call:\n%s", said)
	}
	if !strings.Contains(said, "gave up") {
		t.Errorf("the deadline was not reported:\n%s", said)
	}
}

// The three side requests go out through the client directly, so nothing else
// in the JSONL log sees them. A failed one used to show up only as its
// consequence — a commit with the fallback message, notes that were not there —
// with no record of which model was asked or what came back.

func TestSideCallIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stub     llm.ModelClient
		outcome  string
		attempts int
		wantErr  bool
	}{
		{"success first try", &emptyThenStub{blanks: 0}, "ok", 1, false},
		{"success after a retry", &retryOnceStub{}, "ok", 2, false},
		{"nothing but blanks", &emptyThenStub{blanks: 99}, "empty", maxEmptyRetries + 1, true},
		{"a permanent failure", summaryErrStub{}, "error", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []SideCall
			_, _ = sendSide(context.Background(), tc.stub, llm.Request{Model: "side-slug"},
				"session notes", &summaryOutput{}, &fastClock{}, nil,
				func(s SideCall) { got = append(got, s) })

			if len(got) != 1 {
				t.Fatalf("%d records, want exactly 1 — every exit path reports once", len(got))
			}
			r := got[0]
			if r.Outcome != tc.outcome {
				t.Errorf("outcome = %q, want %q", r.Outcome, tc.outcome)
			}
			if tc.attempts > 0 && r.Attempts != tc.attempts {
				t.Errorf("attempts = %d, want %d", r.Attempts, tc.attempts)
			}
			if r.Attempts < 1 {
				t.Errorf("attempts = %d; a recorded call sent at least one request", r.Attempts)
			}
			if r.What != "session notes" || r.Model != "side-slug" {
				t.Errorf("record does not identify the call: %+v", r)
			}
			if (r.Err != "") != tc.wantErr {
				t.Errorf("err = %q, wantErr = %v", r.Err, tc.wantErr)
			}
		})
	}
}

// A deadline is its own outcome, not lumped in with "error". It is the one the
// budgets in side.go are answerable for, so a log that could not tell it apart
// could not tell anyone whether a budget was set too low.
func TestSideCallRecordsADeadlineAsItsOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var got []SideCall
	_, _ = sendSide(ctx, &emptyThenStub{blanks: 99}, llm.Request{Model: "m"},
		"chat summary", &summaryOutput{}, &fastClock{}, nil,
		func(s SideCall) { got = append(got, s) })

	if len(got) != 1 || got[0].Outcome != "deadline" {
		t.Fatalf("records = %+v, want one with outcome \"deadline\"", got)
	}
}

// Usage is summed across attempts rather than reported for the last one. A call
// that retried twice cost three requests, and a log showing only the third
// understates what the session paid.
func TestSideCallSumsUsageAcrossAttempts(t *testing.T) {
	var got SideCall
	_, _ = sendSide(context.Background(), &usageStub{}, llm.Request{Model: "m"},
		"commit message", &summaryOutput{}, &fastClock{}, nil,
		func(s SideCall) { got = s })

	if got.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", got.Attempts)
	}
	if got.Usage.PromptTokens != 30 || got.Usage.CompletionTokens != 3 {
		t.Errorf("usage = %d/%d, want the sum 30/3 over both attempts",
			got.Usage.PromptTokens, got.Usage.CompletionTokens)
	}
}

// usageStub reports usage on a blank first attempt and on the real second one.
type usageStub struct{ calls int }

func (s *usageStub) Send(context.Context, llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		s.calls++
		text := "ok"
		if s.calls == 1 {
			text = "  "
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: text}, nil) {
			return
		}
		yield(llm.StreamEvent{
			Kind:  llm.EventUsage,
			Usage: &llm.Usage{PromptTokens: 10 * s.calls, CompletionTokens: s.calls},
		}, nil)
	}
}

// The ladder retry_timeout configures. The default has to stay aider's to the
// retry — eight waits, 0.25s doubling to 32s, then give up — because moving the
// cap from the next delay onto the total waited is only meant to change what a
// raised budget does. A raised one keeps retrying at maxRetryDelay until the
// waits reach it, rather than doubling into a single sleep of minutes.
func TestRetryLadder(t *testing.T) {
	ladder := func(budget time.Duration) []time.Duration {
		rb := retryBackoff{delay: initialRetryDelay, cap: budget}
		clock := &fastClock{}
		transient := &llm.StreamError{Class: llm.ErrRateLimit, Message: "429"}
		for rb.retry(context.Background(), &summaryOutput{}, clock, transient) {
			if len(clock.slept) > 100 {
				t.Fatal("the ladder does not terminate")
			}
		}
		return clock.slept
	}
	sum := func(ds []time.Duration) (total time.Duration) {
		for _, d := range ds {
			total += d
		}
		return total
	}

	def := ladder(0)
	if len(def) != 8 || def[len(def)-1] != 32*time.Second {
		t.Errorf("default ladder = %v, want aider's eight waits ending at 32s", def)
	}

	long := ladder(10 * time.Minute)
	for _, d := range long {
		if d > maxRetryDelay {
			t.Errorf("a wait of %v exceeds maxRetryDelay", d)
		}
	}
	if total := sum(long); total < 10*time.Minute || total >= 10*time.Minute+maxRetryDelay {
		t.Errorf("a 10m budget waited %v in total, want at least 10m and less than one more wait past it", total)
	}
}

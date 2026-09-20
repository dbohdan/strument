package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// sendSide makes one side request — a commit message, session
// notes, a compaction summary — with the same transient-error backoff a turn
// gets: retryable StreamErrors (network, rate limit, server) double the delay
// and retry, everything else fails the call.
//
// The side model fails more often than the main one; it is cheap, sometimes
// local (a 503 "Loading model" on first use), and its calls are exactly the
// ones nobody is watching when they go out. Compaction used to be the only
// side call with any recovery, and only turn-level (skip the next attempt);
// the commit message gave up on the first blip and the commit landed with the
// fallback message. Both now ride the same retryBackoff sendMessage does, so a
// slow-to-wake server delays the side call rather than silently degrading it.
//
// The whole attempt, retries included, is bounded by the caller's overall
// timeout via ctx; each retry shares that budget, and an expired context ends
// the call rather than looping forever. The answer is the concatenated
// EventAnswer text; a call that exhausted its retries returns the last error.
// Usage events go to record when non-nil.
func sendSide(
	ctx context.Context,
	cl llm.ModelClient,
	req llm.Request,
	what string,
	out Output,
	clock Clock,
	record func(llm.Usage),
	report SideCallReporter,
) (string, error) {
	backoff := retryBackoff{delay: initialRetryDelay, cap: sideRetryCap, what: what}
	empties := 0
	// The budgets differ per entry point now, so the deadline message reports
	// what this call actually spent rather than naming a constant that belongs
	// to a different caller.
	started := clock.Now()

	// These three requests go out through the client directly, so nothing else
	// in the JSONL log sees them. Without this, a side call that failed left no
	// trace but its absence: a commit with the fallback message, or notes that
	// were simply not there.
	attempts := 0
	var spent llm.Usage
	countUsage := func(u llm.Usage) {
		spent.PromptTokens += u.PromptTokens
		spent.CompletionTokens += u.CompletionTokens
		if u.Cost != nil {
			total := u.Cost
			if spent.Cost != nil {
				sum := *spent.Cost + *u.Cost
				total = &sum
			}
			spent.Cost = total
		}
		if record != nil {
			record(u)
		}
	}
	finish := func(outcome string, err error) {
		if report == nil {
			return
		}
		s := SideCall{
			What: what, Model: req.Model, Duration: clock.Now().Sub(started),
			Attempts: attempts, Outcome: outcome, Usage: spent,
		}
		if err != nil {
			s.Err = err.Error()
		}
		report(s)
	}

	for {
		attempts++
		attempt, err := sideOnce(ctx, cl, req, countUsage)
		if err == nil && strings.TrimSpace(attempt) != "" {
			finish("ok", nil)
			return attempt, nil
		}
		if err == nil {
			empties++
			err = &llm.StreamError{Class: llm.ErrServer, Message: emptySideResponse}
			if empties > maxEmptyRetries {
				out.Errorf("%s: %v", what, err)
				finish("empty", err)
				return "", err
			}
		}
		// ctx is checked inside retry, which sleeps against it and says so when
		// the deadline is what ended the call. It used to be short-circuited
		// here instead, ahead of the one function that prints — so a side call
		// killed by its own timeout returned in silence, and `/notes generate`
		// reported it as "the model returned no notes": a budget we imposed,
		// wearing the costume of a decision the model made.
		if !backoff.retry(ctx, out, clock, err) {
			if ctx.Err() != nil {
				spent := fmt.Errorf("%w after %v", ctx.Err(), clock.Now().Sub(started).Round(time.Second))
				finish("deadline", spent)
				return "", spent
			}
			finish("error", err)
			return "", err
		}
	}
}

// A 200 that streams no content is a failure, not an answer.
//
// It was classified the other way, on the reasoning that calling it a failure
// would turn a terse model into a retry loop. There is no such model here: none
// of these four callers has a use for an empty answer. There is no terse commit
// message with no subject line, no useful summary of nothing, no session note
// that says nothing, and no answer to a /btw that is silence. Terse is one
// line, not zero.
//
// The main path already reads it this way — send.go warns "Empty response
// received from LLM" and fails the send — so the divergence was between two
// halves of the same codebase, and the quiet half was the one nobody watches.
// Live, the side model returned nothing for 21 of roughly 250 commits, each
// landing as "(no commit message provided)": a phrase that reads as a decision
// rather than a failure, with no retry attempted and nothing said.
//
// maxEmptyRetries is deliberately far below the transient-error budget, which
// doubles from 125ms up to a 60s cap and so allows nine attempts. A network
// blip earns those because the next attempt plausibly succeeds. A provider
// answering 200-with-nothing twice running is not warming up, and each further
// attempt is another paid request for the same nothing.
const (
	maxEmptyRetries   = 2
	emptySideResponse = "the model returned an empty response"

	// sideTimeout is the floor every side-call budget is built from, and the
	// budget for a call with nothing more specific to say about itself.
	//
	// It was 60s for all three calls, and 60s was too small by a wide margin.
	// Over 72 live calls across four side models and six input shapes
	// (doc/experiments/2026-09-side-call-timing/), a 60s budget cuts 62% of
	// compaction calls and 31% of commit-message calls — and none of those were
	// stalls. The largest gap between bytes in the whole sample was 10.8s; the
	// calls were simply long, because a side model reasons before it answers
	// whether or not anyone asked it to (see the note on ReasoningEffort in
	// commit.go). What looked like a flaky side model was a deadline set below
	// the work.
	//
	// The per-call budgets below are not a new mechanism. A stalled stream is
	// already caught upstream by the client's idle timeout, which bounds the gap
	// between bytes rather than the duration of the call
	// (internal/client/idle.go) and covers side calls because they share the
	// client. These budgets exist for the failure an idle timeout cannot see: a
	// model that streams steadily and never stops. One call in the sample spent
	// 845.9s and 36,141 characters of reasoning to produce a 202-character
	// commit message, with a maximum gap of 0.8s. Nothing watching for silence
	// would ever have cut it.
	sideTimeout = 120 * time.Second

	// summaryTimeoutBudget is larger than the rest because compaction's failure
	// is the most expensive of the three. A commit message falls back to a
	// generated one and notes are simply absent, but a fold that does not happen
	// leaves the history oversized, so the *next* send is the one that fails.
	//
	// 300s cuts 4% of the summary calls in the sample against 17% at 180s and
	// 33% at 120s. The 4% is not a number to chase to zero: the slowest call
	// measured ran 675.8s, and a fold nobody is willing to wait eleven minutes
	// for is one worth abandoning. What the budget buys is that the *typical*
	// long fold — the sample's median is 94.1s — now completes, where under 60s
	// it did not.
	summaryTimeoutBudget = 300 * time.Second

	// sideRetryCap bounds the backoff for a side call, and is small on purpose.
	//
	// The turn's own cap is retryTimeout, 60s, which makes the ladder sleep
	// 0.25+0.5+1+2+4+8+16+32 = 63.75s if it runs to the end. Under a 60s
	// deadline that ladder cannot finish: the context died partway, the call
	// returned nothing, and the retries it had left were never going to happen.
	// The arithmetic was the bug, not the retrying.
	//
	// At 8s the ladder sleeps 0.25+0.5+1+2+4+8 = 15.75s over six retries,
	// leaving the rest of the budget for the seven requests themselves. Giving
	// up sooner is also the right shape for a side call: a user waiting on
	// their own prompt is better served by notes that fail in twenty seconds
	// than by notes that arrive in two minutes.
	//
	// sideLadderFitsItsBudget holds this to sideTimeout, which is the smallest
	// of the budgets, so it fits in all of them.
	sideRetryCap = 8 * time.Second
)

// sideOnce runs one attempt and returns its answer text. Blank text with a nil
// error is left for sendSide to classify.
func sideOnce(ctx context.Context, cl llm.ModelClient, req llm.Request, record func(llm.Usage)) (string, error) {
	var answer strings.Builder
	for ev, err := range cl.Send(ctx, req) {
		if err != nil {
			return "", err
		}
		switch ev.Kind {
		case llm.EventAnswer:
			answer.WriteString(ev.Text)
		case llm.EventUsage:
			if ev.Usage != nil && record != nil {
				record(*ev.Usage)
			}
		}
	}
	return answer.String(), nil
}

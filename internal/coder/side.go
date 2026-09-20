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
) (string, error) {
	backoff := retryBackoff{delay: initialRetryDelay, cap: sideRetryCap, what: what}
	empties := 0
	for {
		attempt, err := sideOnce(ctx, cl, req, record)
		if err == nil && strings.TrimSpace(attempt) != "" {
			return attempt, nil
		}
		if err == nil {
			empties++
			err = &llm.StreamError{Class: llm.ErrServer, Message: emptySideResponse}
			if empties > maxEmptyRetries {
				out.Errorf("%s: %v", what, err)
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
				return "", fmt.Errorf("%w after %v", ctx.Err(), sideTimeout)
			}
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

	// sideTimeout bounds one side call, retries included. Every side call uses
	// it, so the ladder below can be sized against one number.
	sideTimeout = 60 * time.Second

	// sideRetryCap bounds the backoff for a side call, and is small on purpose.
	//
	// The turn's own cap is retryTimeout, 60s, which makes the ladder sleep
	// 0.25+0.5+1+2+4+8+16+32 = 63.75s if it runs to the end. Under a 60s
	// deadline that ladder cannot finish: the context died partway, the call
	// returned nothing, and the retries it had left were never going to happen.
	// The arithmetic was the bug, not the retrying.
	//
	// At 8s the ladder sleeps 0.25+0.5+1+2+4+8 = 15.75s over six retries,
	// leaving the rest of the minute for the seven requests themselves. Giving
	// up sooner is also the right shape for a side call: a user waiting on
	// their own prompt is better served by notes that fail in twenty seconds
	// than by notes that arrive in two minutes.
	//
	// sideLadderFitsItsBudget holds this to sideTimeout.
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

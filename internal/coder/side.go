package coder

import (
	"context"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// sendSide makes one weak-model side request — a commit message, session
// notes, a compaction summary — with the same transient-error backoff a turn
// gets: retryable StreamErrors (network, rate limit, server) double the delay
// and retry, everything else fails the call.
//
// The weak model fails more often than the main one; it is cheap, sometimes
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
	out Output,
	clock Clock,
	record func(llm.Usage),
) (string, error) {
	backoff := retryBackoff{delay: initialRetryDelay}
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
				out.Errorf("%v", err)
				return "", err
			}
		}
		if ctx.Err() != nil || !backoff.retry(out, clock, err) {
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
// Live, the weak model returned nothing for 21 of roughly 250 commits, each
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

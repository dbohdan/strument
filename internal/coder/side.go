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
	for {
		attempt, err := sideOnce(ctx, cl, req, record)
		if err != nil {
			if ctx.Err() != nil || !backoff.retry(out, clock, err) {
				return "", err
			}
			continue
		}
		return attempt, nil
	}
}

// sideOnce runs one attempt and returns its answer text. Empty text with a nil
// error is a real (if useless) answer, not a failure — classifying it as one
// would turn a terse model into a retry loop.
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

package coder

import (
	"context"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// RunAside answers a one-off question with no chat context, no file context, and
// no coder system prompt — a general-assistant reply, not a software-developer
// one. It streams the answer (and any reasoning) like a normal turn and reports
// usage the same way, so the session cost advances, but nothing is added to the
// conversation. It returns the answer text.
//
// This backs the REPL's /btw. Unlike sendMessage it never touches curMessages
// or doneMessages, sends no tools, and does not continue or reflect — a
// throwaway question stays a single, isolated exchange. It does share the same
// stream + transient-error backoff, so /btw retries a slow-to-wake server (a
// 503 "Loading model") exactly like a normal turn.
func (c *Coder) RunAside(ctx context.Context, question string) string {
	if strings.TrimSpace(question) == "" {
		return ""
	}

	messages := []llm.Message{llm.TextMessage("user", question)}
	req := llm.Request{
		Model:           c.Model.Slug,
		Messages:        messages,
		Temperature:     c.Model.Temperature,
		ReasoningEffort: c.Model.Reasoning,
		ExtraParams:     c.Model.RequestExtraParams(),
		// Deliberately no system message and no tools: a plain question.
	}

	// The stream writes through the same transient fields finalizeUsage reads
	// for its aborted-turn estimate; clear them here and again at the end so a
	// /btw never leaks state into the next real turn.
	c.multiResponseContent = ""

	usage := &sendUsage{estSent: c.countMessages(messages)}
	backoff := retryBackoff{delay: initialRetryDelay}
	for {
		c.partialResponseContent = ""
		c.partialReasoningContent = ""
		c.partialToolCalls = nil
		c.toolCallIndex = map[int]int{}

		res, streamErr := c.streamOnce(ctx, req, usage)
		if res == resFailed {
			if backoff.retry(c, streamErr) {
				continue // transient error: retry with the same backoff as a turn
			}
			break
		}
		break // done, interrupted, or truncated: a one-off does not continue
	}
	c.Out.FlushStream()

	answer := stripReasoning(c.partialResponseContent, c.Model.ReasoningTag)
	c.partialResponseContent = answer // for finalizeUsage's estimate fallback
	c.finalizeUsage(usage)

	c.multiResponseContent = ""
	c.partialResponseContent = ""
	c.partialReasoningContent = ""
	return answer
}

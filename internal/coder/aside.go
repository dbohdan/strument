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
	return c.runSide(ctx, "aside", question, sideFrame{open: "\u2039btw\u203a", tag: "btw"})
}

// sideFrame brackets a side call's answer on screen. The answer streams
// rendered like a reply, and a consulted model's page of Markdown is
// indistinguishable from the session model's own; a usage line after it was
// the only boundary, and the same line follows the session model's replies.
// The closer is named rather than thinking's bare ‹/› because the answer can
// hold a thinking block of its own, and two identical closers in a row would
// leave the reader to count. It carries the model and the cost, so nothing
// after it reads as part of the answer.
type sideFrame struct {
	open string // the opening line: the tag and anything after it
	tag  string // the closer's name, and the cost line's label
	who  string // who answered, for the closer; the model's slug when empty
}

// runSide is the side call itself, shared with the consult tool. It sends
// exactly the prompt it is given as a single user message against whatever
// Client and Model are in place, so a caller that wants a different model swaps
// those two and this stays ignorant of the difference. call names the request
// in the session record.
func (c *Coder) runSide(ctx context.Context, call, prompt string, frame sideFrame) string {
	c.Out.Toolf("%s", frame.open)
	before := c.usage
	messages := []llm.Message{llm.TextMessage("user", prompt)}
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
	c.stream.multi = ""

	usage := &sendUsage{estSent: c.countMessages(messages)}
	backoff := retryBackoff{delay: initialRetryDelay, cap: c.RetryTimeout}
	for {
		c.stream.response = ""
		c.stream.reasoning = ""
		c.stream.toolCalls = nil
		c.stream.toolCallIndex = map[int]int{}

		// No loop detection on a side call: these are short, structured, and
		// bounded by their own prompts, and a false stop here would silently
		// cost a summary rather than visibly stop a reply.
		res, streamErr := c.streamOnce(ctx, call, req, usage, nil)
		if res == resFailed {
			if backoff.retry(ctx, c.Out, c.Clock, streamErr) {
				continue // transient error: retry with the same backoff as a turn
			}
			break
		}
		break // done, interrupted, or truncated: a one-off does not continue
	}
	c.Out.FlushStream()

	answer := stripReasoning(c.stream.response, c.Model.ReasoningTag)
	c.stream.response = answer // for finalizeUsage's estimate fallback
	c.finalizeUsage(usage)
	c.closeSide(frame, before)
	c.reportUsage(1, 0)

	c.stream.multi = ""
	c.stream.response = ""
	c.stream.reasoning = ""
	return answer
}

// closeSide writes the frame's closer: who answered, and what it cost.
//
// The cost is this side call's alone, measured as what its sends added to the
// message totals: a consult starts them at zero, but a /btw runs between turns
// on top of the last turn's, and reading the totals as they stood printed that
// turn's tokens and cost under the aside's name.
func (c *Coder) closeSide(frame sideFrame, before messageUsage) {
	who := frame.who
	if who == "" {
		who = c.Model.QualifiedSlug()
	}
	d := c.usage.minus(before)
	report := strings.TrimPrefix(usageReport(frame.tag, "", d.sent, d.cacheWrite, d.cacheRead, d.received,
		d.modelTime, d.cost, d.costKnown, c.totalCost, d.estimated), "Tokens: ")
	c.Out.Toolf("\u2039/%s\u203a %s: %s", frame.tag, who, report)
}

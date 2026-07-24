package coder

import (
	"context"
	"errors"
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
// or doneMessages, sends no tools, and does not reflect or retry — a throwaway
// question deserves a single, isolated exchange.
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
	// for its aborted-turn estimate; clear them first and again at the end so a
	// /btw never leaks state into the next real turn.
	c.multiResponseContent = ""
	c.partialResponseContent = ""
	c.partialReasoningContent = ""

	usage := &sendUsage{estSent: c.countMessages(messages)}
	for ev, err := range c.Client.Send(ctx, req) {
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				break // interrupted: stop quietly, like a normal turn's Ctrl-C
			}
			c.Out.Errorf("%v", err)
			break
		}
		switch ev.Kind {
		case llm.EventAnswer:
			c.partialResponseContent += ev.Text
			c.Out.StreamText(ev.Text)
		case llm.EventReasoning:
			c.partialReasoningContent += ev.Text
			c.Out.StreamReasoning(ev.Text)
		case llm.EventUsage:
			usage.add(ev.Usage)
		}
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

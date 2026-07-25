package coder

import (
	"context"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// Summarization tunables, ported from aider's ChatSummary (history.py).
const (
	summaryTimeout       = 60 * time.Second
	summaryMinSplit      = 4    // below this many messages, summarize the lot
	summaryMaxDepth      = 3    // recursion cap before summarizing the lot
	summaryInputBuffer   = 512  // reserved from the weak model's window per call
	summaryFallbackInput = 4096 // weak-model window when its Context is unknown
)

// ChatSummary compacts settled chat history by asking the weak model to
// summarize the older messages, so a long session stays within the main
// model's context window. It is a close port of aider's ChatSummary — recursive
// split: keep a recent tail, summarize the older head, recurse — run
// synchronously by the coder.
type ChatSummary struct {
	client llm.ModelClient
	weak   *config.Model
	tokens TokenCounter
}

// NewChatSummary builds a summarizer backed by the weak model.
func NewChatSummary(client llm.ModelClient, weak *config.Model, tokens TokenCounter) *ChatSummary {
	return &ChatSummary{client: client, weak: weak, tokens: tokens}
}

func (s *ChatSummary) count(m llm.Message) int { return s.tokens.Count(m.Text()) }

func (s *ChatSummary) total(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += s.count(m)
	}
	return n
}

// weakInputBound is how much head the weak model can be fed in one call.
func (s *ChatSummary) weakInputBound() int {
	if s.weak != nil && s.weak.Context > 0 {
		return s.weak.Context
	}
	return summaryFallbackInput
}

// tooBig reports whether the messages exceed the history budget.
func (s *ChatSummary) tooBig(msgs []llm.Message, maxTokens int) bool {
	return s.total(msgs) > maxTokens
}

// summarize compacts msgs to fit maxTokens (the main model's history budget),
// always ending with an assistant "Ok." so the slot stays a clean
// user/assistant pair. On a weak-model failure it returns msgs unchanged and
// the error, so the caller can warn and leave history intact.
func (s *ChatSummary) summarize(msgs []llm.Message, maxTokens int) ([]llm.Message, error) {
	out, err := s.summarizeReal(msgs, maxTokens, 0)
	if err != nil {
		return msgs, err
	}
	if len(out) > 0 && out[len(out)-1].Role != llm.RoleAssistant {
		out = append(out, llm.TextMessage(llm.RoleAssistant, "Ok."))
	}
	return out, nil
}

func (s *ChatSummary) summarizeReal(msgs []llm.Message, maxTokens, depth int) ([]llm.Message, error) {
	if depth == 0 && s.total(msgs) <= maxTokens {
		return msgs, nil
	}
	if len(msgs) <= summaryMinSplit || depth > summaryMaxDepth {
		return s.summarizeAll(msgs)
	}

	// Pick a split so the preserved tail sums to under half the budget, then
	// back up so the summarized head ends on an assistant boundary.
	half := maxTokens / 2
	tailTokens := 0
	split := len(msgs)
	for i, m := range slices.Backward(msgs) {
		t := s.count(m)
		if tailTokens+t >= half {
			break
		}
		tailTokens += t
		split = i
	}
	for split > 1 && msgs[split-1].Role != llm.RoleAssistant {
		split--
	}
	if split <= summaryMinSplit {
		return s.summarizeAll(msgs)
	}

	tail := msgs[split:]

	// Feed the weak model as much of the head as its window allows.
	bound := s.weakInputBound() - summaryInputBuffer
	var keep []llm.Message
	acc := 0
	for _, m := range msgs[:split] {
		acc += s.count(m)
		if acc > bound {
			break
		}
		keep = append(keep, m)
	}

	summary, err := s.summarizeAll(keep)
	if err != nil {
		return nil, err
	}

	combined := make([]llm.Message, 0, len(summary)+len(tail))
	combined = append(combined, summary...)
	combined = append(combined, tail...)
	if s.total(summary)+s.total(tail) < maxTokens {
		return combined, nil
	}
	return s.summarizeReal(combined, maxTokens, depth+1)
}

// summarizeAll collapses msgs into a single summary user message via the weak
// model, mirroring CommitMessenger's send loop (commit.go).
func (s *ChatSummary) summarizeAll(msgs []llm.Message) ([]llm.Message, error) {
	var content strings.Builder
	for _, m := range msgs {
		role := strings.ToUpper(m.Role)
		if role != "USER" && role != "ASSISTANT" {
			continue
		}
		content.WriteString("# " + role + "\n")
		text := m.Text()
		content.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			content.WriteString("\n")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), summaryTimeout)
	defer cancel()

	var answer strings.Builder
	for ev, err := range s.client.Send(ctx, llm.Request{
		Model: s.weak.Slug,
		Messages: []llm.Message{
			llm.TextMessage(llm.RoleSystem, prompts.Summarize),
			llm.TextMessage(llm.RoleUser, content.String()),
		},
		ReasoningEffort: s.weak.Reasoning,
		Temperature:     s.weak.Temperature,
		ExtraParams:     s.weak.RequestExtraParams(),
	}) {
		if err != nil {
			return nil, err
		}
		if ev.Kind == llm.EventAnswer {
			answer.WriteString(ev.Text)
		}
	}

	summary := prompts.SummaryPrefix + strings.TrimSpace(answer.String())
	return []llm.Message{llm.TextMessage(llm.RoleUser, summary)}, nil
}

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
	summaryInputBuffer   = 512  // reserved from the side model's window per call
	summaryFallbackInput = 4096 // side-model window when its Context is unknown
	// summaryToolBytes caps one tool result fed to the summarizer. A read of a
	// large file would otherwise fill the side model's window with the very
	// content the prompt asks it to leave out.
	summaryToolBytes = 2000
)

// ChatSummary compacts settled chat history by asking the side model to
// summarize the older messages, so a long session stays within the main
// model's context window. It is a close port of aider's ChatSummary — recursive
// split: keep a recent tail, summarize the older head, recurse — run
// synchronously by the coder.
type ChatSummary struct {
	client llm.ModelClient
	side   *config.Model
	tokens TokenCounter
	out    Output
	clock  Clock
}

// NewChatSummary builds a summarizer backed by the side model. out and clock
// are the same ports the coder talks through, so a compaction retry reports
// and sleeps exactly like a turn's.
func NewChatSummary(client llm.ModelClient, side *config.Model, tokens TokenCounter, out Output, clock Clock) *ChatSummary {
	return &ChatSummary{client: client, side: side, tokens: tokens, out: out, clock: clock}
}

func (s *ChatSummary) count(m llm.Message) int { return s.tokens.Count(m.Text()) }

func (s *ChatSummary) total(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += s.count(m)
	}
	return n
}

// sideInputBound is how much head the side model can be fed in one call.
func (s *ChatSummary) sideInputBound() int {
	if s.side != nil && s.side.Context > 0 {
		return s.side.Context
	}
	return summaryFallbackInput
}

// tooBig reports whether the messages exceed the history budget.
func (s *ChatSummary) tooBig(msgs []llm.Message, maxTokens int) bool {
	return s.total(msgs) > maxTokens
}

// summarize compacts msgs to fit maxTokens (the main model's history budget).
// On a side-model failure it returns msgs unchanged and the error, so the
// caller can warn and leave history intact.
//
// It used to append an assistant "Ok." here, to keep the slot a clean
// user/assistant pair — necessary only because the summary itself was a user
// message. The summary is a system message now, which needs no partner, so the
// fabricated agreement goes with it.
func (s *ChatSummary) summarize(msgs []llm.Message, maxTokens int) ([]llm.Message, error) {
	out, err := s.summarizeReal(msgs, maxTokens, 0)
	if err != nil {
		return msgs, err
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

	// Feed the side model as much of the head as its window allows.
	bound := s.sideInputBound() - summaryInputBuffer
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

// summarizeAll collapses msgs into a single summary system message via the side
// model, riding the same side-call retry as the commit message (side.go).
func (s *ChatSummary) summarizeAll(msgs []llm.Message) ([]llm.Message, error) {
	content := renderForSummary(msgs)

	ctx, cancel := context.WithTimeout(context.Background(), summaryTimeout)
	defer cancel()

	answer, err := sendSide(ctx, s.client, llm.Request{
		Model: s.side.Slug,
		Messages: []llm.Message{
			llm.TextMessage(llm.RoleSystem, prompts.Summarize),
			llm.TextMessage(llm.RoleUser, content),
		},
		ReasoningEffort: s.side.Reasoning,
		Temperature:     s.side.Temperature,
		ExtraParams:     s.side.RequestExtraParams(),
	}, s.out, s.clock, nil)
	if err != nil {
		return nil, err
	}

	summary := prompts.SummaryLabel + strings.TrimSpace(answer)
	return []llm.Message{llm.TextMessage(llm.RoleSystem, summary)}, nil
}

// renderForSummary lays the messages out for the side model.
//
// It used to skip every role that was not USER or ASSISTANT, which in a harness
// where everything a turn does arrives as tool calls meant the summarizer saw
// only the thin prose layer: a turn that made twelve tool calls and closed with
// one sentence compacted to that sentence. The work was invisible to the thing
// summarizing the work.
//
// Three consequences of including the rest:
//
// Tool *calls* carry the intent — which file, which pattern — so they are shown
// in full. Tool *results* carry file contents and search output, which is
// exactly what the prompt asks the model to drop, so they are cut to a budget:
// enough to see what came back, not enough to pay for it twice.
//
// System messages have to be included, and this is the subtle one. Once the
// summary itself became a system message, skipping the role would have silently
// dropped every earlier summary on a second pass — compaction erasing its own
// output, which is worse than the fold it replaced.
func renderForSummary(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		body := strings.TrimRight(m.Text(), "\n")
		switch m.Role {
		case llm.RoleTool:
			body = clipForSummary(body)
		case llm.RoleAssistant:
			for _, tc := range m.ToolCalls {
				if body != "" {
					body += "\n"
				}
				body += "calls " + tc.Name + " " + clipForSummary(tc.Arguments)
			}
		}
		if body == "" {
			continue
		}
		b.WriteString("# " + strings.ToUpper(m.Role) + "\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// clipForSummary caps one tool result or argument blob. The cut is announced so
// the model reads a truncated result as truncated rather than as a file that
// ends there — the same reason maxToolOutputBytes says so (toolobserve.go).
func clipForSummary(s string) string {
	if len(s) <= summaryToolBytes {
		return s
	}
	return s[:summaryToolBytes] + "\n… (cut; the full result is not part of the summary input)"
}

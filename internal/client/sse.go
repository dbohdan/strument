package client

import (
	"bufio"
	"encoding/json"
	"io"
	"iter"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// sseChunk is one OpenAI-dialect streaming chunk.
type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`         // OpenRouter native reasoning
			ReasoningContent string `json:"reasoning_content"` // OpenAI-compat variant
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int      `json:"prompt_tokens"`
		CompletionTokens    int      `json:"completion_tokens"`
		Cost                *float64 `json:"cost"` // OpenRouter in-band cost
		PromptTokensDetails *struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// scanSSEData yields the payload of each "data:" line, in wire order, and
// stops at the "[DONE]" sentinel. Both dialects share it: the framing is
// server-sent events either way, and only what the payloads mean differs.
// Anthropic's stream also carries "event:" lines, which are redundant with the
// "type" field inside each payload and are skipped here.
func scanSSEData(r io.Reader) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		scan := bufio.NewScanner(r)
		scan.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scan.Scan() {
			line := scan.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimSpace(line[len("data: "):])
			if payload == "[DONE]" {
				return
			}
			if !yield(payload, nil) {
				return
			}
		}
		// A read that fails partway is a cut-off stream, not a finished one.
		if err := scan.Err(); err != nil {
			yield("", &llm.StreamError{Class: llm.ErrNetwork, Message: err.Error()})
		}
	}
}

// ParseSSE converts an OpenAI-dialect SSE stream into StreamEvents, in wire
// order. This parser is the single source of dialect truth: the coder
// consumes it live and `strumentrec distill` uses it to turn raw captures
// into fixture rows.
func ParseSSE(r io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for payload, err := range scanSSEData(r) {
			if err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			var chunk sseChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrServer, Message: "bad SSE chunk: " + err.Error()})
				return
			}
			// Providers can surface errors mid-stream as an error object.
			if chunk.Error != nil {
				yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrServer, Message: chunk.Error.Message})
				return
			}
			for _, choice := range chunk.Choices {
				if choice.Delta.Reasoning != "" {
					if !yield(llm.StreamEvent{Kind: llm.EventReasoning, Text: choice.Delta.Reasoning}, nil) {
						return
					}
				}
				if choice.Delta.ReasoningContent != "" {
					if !yield(llm.StreamEvent{Kind: llm.EventReasoning, Text: choice.Delta.ReasoningContent}, nil) {
						return
					}
				}
				if choice.Delta.Content != "" {
					if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: choice.Delta.Content}, nil) {
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
						Index: tc.Index,
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Args:  tc.Function.Arguments,
					}}, nil) {
						return
					}
				}
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					if !yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: *choice.FinishReason}, nil) {
						return
					}
				}
			}
			if chunk.Usage != nil {
				u := &llm.Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
				}
				if d := chunk.Usage.PromptTokensDetails; d != nil {
					u.CacheReadTokens = d.CachedTokens
					u.CacheWriteTokens = d.CacheWriteTokens
				}
				if chunk.Usage.Cost != nil {
					cost := *chunk.Usage.Cost
					u.Cost = &cost
				}
				if !yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: u}, nil) {
					return
				}
			}
		}
	}
}

// Package llm holds the wire-neutral chat types shared by the client, the
// base coder, and the fixture replay harness: messages, stream events, usage,
// and money.
package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Message roles. Plain strings on the wire.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one chat message. Content is structured, not any. An assistant
// message may carry ToolCalls; a RoleTool message carries the result of one
// call, keyed by ToolCallID.
type Message struct {
	Role       string
	Content    Content
	ToolCalls  []ToolCall // assistant tool calls
	ToolCallID string     // set on RoleTool result messages
}

// ToolCall is one function call the model requested. Arguments is the raw
// JSON string as the model produced it (parsed by the caller).
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolResult builds a RoleTool result message for a given call id.
func ToolResult(callID, text string) Message {
	return Message{Role: RoleTool, Content: TextContent(text), ToolCallID: callID}
}

// Text returns the message content flattened to a string.
func (m Message) Text() string { return m.Content.String() }

// TextMessage builds a plain-text message.
func TextMessage(role, text string) Message {
	return Message{Role: role, Content: TextContent(text)}
}

// Content is either a plain string or a list of blocks (needed once
// cache-control decoration applies).
//
// Content by value); json.Unmarshaler requires a pointer receiver.
//
//nolint:recvcheck // json.Marshaler needs a value receiver (Message embeds
type Content struct {
	Text   *string
	Blocks []ContentBlock
}

// TextContent wraps a string as plain-text content.
func TextContent(s string) Content { return Content{Text: &s} }

func (c Content) String() string {
	if c.Text != nil {
		return *c.Text
	}
	var out strings.Builder
	for _, b := range c.Blocks {
		out.WriteString(b.Text)
	}
	return out.String()
}

func (c Content) MarshalJSON() ([]byte, error) {
	if c.Text != nil {
		return json.Marshal(*c.Text)
	}
	return json.Marshal(c.Blocks)
}

func (c *Content) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.Text = &s
		c.Blocks = nil
		return nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return fmt.Errorf("content is neither string nor block list: %w", err)
	}
	c.Text = nil
	c.Blocks = blocks
	return nil
}

// ContentBlock is one block of structured message content.
type ContentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl marks a prompt-cache breakpoint.
type CacheControl struct {
	Type string `json:"type"`          // "ephemeral"
	TTL  string `json:"ttl,omitempty"` // "1h" extended cache; "" => provider default (~5m)
}

// EventKind tags a StreamEvent.
type EventKind string

const (
	EventAnswer    EventKind = "Answer"
	EventReasoning EventKind = "Reasoning"
	EventUsage     EventKind = "Usage"
	EventFinish    EventKind = "Finish"
	EventToolCall  EventKind = "ToolCall"
)

// StreamEvent is one event from a model response stream.
// Errors travel on the error side of
// iter.Seq2[StreamEvent, error], not as events.
type StreamEvent struct {
	Kind         EventKind      `json:"kind"`
	Text         string         `json:"text,omitempty"`
	Usage        *Usage         `json:"usage,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
	ToolCall     *ToolCallDelta `json:"tool_call,omitempty"`
}

// ToolCallDelta is a streamed fragment of a tool call. ID and Name arrive on
// the first fragment for an index; later fragments carry only Args chunks.
type ToolCallDelta struct {
	Index int    `json:"index"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Args  string `json:"args,omitempty"`
}

// Usage is token/cost accounting for one request, with each token class
// tracked independently.
type Usage struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	CacheWriteTokens int      `json:"cache_write_tokens,omitempty"`
	CacheReadTokens  int      `json:"cache_read_tokens,omitempty"`
	Cost             *float64 `json:"cost,omitempty"` // in-band cost (OpenRouter); nil => unknown
}

// Add accumulates u2 into u. A known cost adds to a known (or zero) cost;
// an unknown cost on either side leaves Cost nil only if u had no known cost
// to begin with — callers that need known/unknown semantics per request
// should inspect each Usage before accumulating.
func (u *Usage) Add(u2 Usage) {
	u.PromptTokens += u2.PromptTokens
	u.CompletionTokens += u2.CompletionTokens
	u.CacheWriteTokens += u2.CacheWriteTokens
	u.CacheReadTokens += u2.CacheReadTokens
	if u2.Cost != nil {
		if u.Cost == nil {
			c := *u2.Cost
			u.Cost = &c
		} else {
			*u.Cost += *u2.Cost
		}
	}
}

// Money is an amount that may be unknown. Never fabricate $0 for unknown.
type Money struct {
	Known bool
	USD   float64
}

func (m Money) String() string {
	if !m.Known {
		return "unknown"
	}
	return fmt.Sprintf("$%.6f", m.USD)
}

// ErrorClass classifies a provider/stream failure; it drives the retry table
// and the fixture error rows.
type ErrorClass string

const (
	ErrNetwork       ErrorClass = "network"
	ErrRateLimit     ErrorClass = "rate_limit"
	ErrContextWindow ErrorClass = "context_window"
	ErrAuth          ErrorClass = "auth"
	ErrServer        ErrorClass = "server"
)

// StreamError is a classified failure surfaced by a model stream.
type StreamError struct {
	Class   ErrorClass
	Message string
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("%s: %s", e.Class, e.Message)
}

// Retryable reports whether the retry loop should retry this failure
// (transient errors retry with doubling delay).
func (e *StreamError) Retryable() bool {
	switch e.Class {
	case ErrNetwork, ErrRateLimit, ErrServer:
		return true
	default:
		return false
	}
}

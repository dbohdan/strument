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

// WireArguments is Arguments in a form a provider will accept: the model's own
// string when it parses as JSON, and "{}" when it does not.
//
// Providers validate this field, and a request carrying one malformed call is
// rejected whole — so a single bad call does not cost one tool result, it ends
// every remaining turn of the session. Observed against OpenRouter, which
// answers `{"error":{"code":502,"message":"Upstream error from DeepInfra:
// Assistant tool call function.arguments must be valid JSON."}}` and keeps
// answering it, because the malformed message is in the history now and goes
// out with every subsequent request.
//
// Two ways to get one. The empty string, which some models send for a tool that
// takes no arguments and which is not valid JSON — the Anthropic and Responses
// clients each already worked that out and fixed it locally, which is how the
// chat-completions client came to be the one that still sent it. And a call cut
// off mid-argument when the reply hits the output limit, which is the same
// defect and is not fixed by a non-empty check.
//
// Repaired here, at the wire, rather than in the conversation: the raw string
// is what the model produced, the tool dispatch has to see it to tell the model
// what was wrong with it, and the transcript should record what happened rather
// than a tidied version of it.
func (t ToolCall) WireArguments() string {
	if json.Valid([]byte(t.Arguments)) {
		return t.Arguments
	}
	return "{}"
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

// HarnessMarker prefixes anything the harness itself says inside a
// conversation, so neither the model nor a reader of the transcript can mistake
// it for something the human typed.
const HarnessMarker = "[strument]"

// HarnessNote is the harness speaking into an ongoing conversation: "the reply
// was interrupted", "that tool call was not run". It is a *user*-role message,
// and the role is a wire constraint rather than a preference.
//
// The honest role would be system, and system is what this package uses for the
// harness's voice in the prefix — session notes, a history summary. It cannot
// be used mid-conversation. Anthropic rejects a system message that follows an
// assistant turn outright:
//
//	messages.2: role 'system' must follow a 'user' message or an 'assistant'
//	message ending in a server tool result
//
// and "after an assistant turn" is exactly where an interruption note has to
// go. A live probe across five providers found this shape accepted by all of
// them and heeded as well as a system message was by the four that accepted
// one, so the marker carries the meaning the role cannot.
//
// The rule this encodes, worth keeping whole: the system role belongs to the
// prefix; anything the harness says once the conversation is under way is a
// marked user turn. What it must never be is an assistant turn — putting words
// in the model's mouth is the one thing no provider will catch for us.
func HarnessNote(text string) Message {
	return TextMessage(RoleUser, HarnessMarker+" "+text)
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
		out.WriteString(b.String())
	}
	return out.String()
}

// Images returns the image payloads in order, for the callers that have to
// account for them separately: the token estimator and the capability
// projection.
func (c Content) Images() []ImageSource {
	var out []ImageSource
	for _, b := range c.Blocks {
		if b.Type == BlockImage && b.Image != nil {
			out = append(out, *b.Image)
		}
	}
	return out
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

// Content block kinds. A block carries exactly one payload, selected by Type.
//
// Every kind added here must gain a case in both wire clients. Neither one can
// fall through to a default that drops the block: an image the provider never
// received produces a plausible answer rather than an error, so the user reads
// "I cannot see an image" as the model being bad at vision. blockkind_test.go
// fails the build if a kind is missing from either client.
const (
	BlockText  = "text"
	BlockImage = "image"
)

// ContentBlock is one block of structured message content.
type ContentBlock struct {
	Type string `json:"type"`
	// Text is set when Type is BlockText. It carries omitempty so a block of
	// another kind does not go out with a meaningless `"text":""` beside its
	// own payload.
	Text         string        `json:"text,omitempty"`
	Image        *ImageSource  `json:"image,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageSource is one image in a message, held as base64 rather than as a path
// so the bytes that were read are the bytes that go out: a file that changes
// between the read and the send cannot make the label disagree with the
// payload.
type ImageSource struct {
	MediaType string `json:"media_type"` // "image/png", "image/jpeg", ...
	Data      string `json:"data"`       // base64, with no data: URI prefix
	// Label names the image in prose: the file it came from. It is what the
	// text projection shows a model that cannot see images, and what the
	// transcript shows a human.
	Label  string `json:"label,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// TextBlock builds a text block. Use it rather than a literal so a new kind
// cannot be introduced by a caller that forgets to set Type.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: BlockText, Text: text}
}

// ImageBlock builds an image block.
func ImageBlock(src ImageSource) ContentBlock {
	return ContentBlock{Type: BlockImage, Image: &src}
}

// BlocksContent wraps blocks as message content.
func BlocksContent(blocks ...ContentBlock) Content { return Content{Blocks: blocks} }

// String renders one block as text.
//
// An image renders as its label rather than as the empty string, and that is
// the whole reason this method exists. Every existing caller of Message.Text()
// — the token estimator in assemble.go, ChatSummary.count, renderForSummary —
// sums or renders Content.String(), so a block that stringified to "" was a
// silent zero in three separate accountings and a silent drop in the fourth.
// A label is not the image, but it is true, and it is countable.
func (b ContentBlock) String() string {
	switch b.Type {
	case BlockText:
		return b.Text
	case BlockImage:
		if b.Image == nil {
			return "[image: missing]"
		}
		return b.Image.String()
	default:
		// Never the empty string, and that is not tidiness. splitInstructions
		// in the Responses client drops a message whose content stringifies to
		// "", so a kind that rendered as nothing here was a whole user turn
		// silently removed from the request — found by the in-band test in
		// internal/client/imageblock_test.go, not by reading this.
		return "[strument: unsupported content block " + b.Type + "]"
	}
}

// String describes an image in one bracketed phrase, sized when the dimensions
// are known.
func (s ImageSource) String() string {
	label := s.Label
	if label == "" {
		label = s.MediaType
	}
	if s.Width > 0 && s.Height > 0 {
		return fmt.Sprintf("[image: %s, %dx%d]", label, s.Width, s.Height)
	}
	return fmt.Sprintf("[image: %s]", label)
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
	// ErrRequest is the request itself being wrong — a model slug that does
	// not exist, an unsupported parameter, an account out of credit. The
	// provider will answer a retry the same way, so retrying only delays the
	// message the user needs to read.
	ErrRequest ErrorClass = "request"
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

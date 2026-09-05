package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/http"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/llm"
)

// The Anthropic Messages dialect. It is a different protocol from
// chat-completions rather than a variation on it, so it gets its own client
// instead of a branch inside the OpenAI one: the system prompt is a top-level
// field, content is a list of typed blocks, a tool result travels as a block
// inside a *user* message, and the stream is six event types rather than a
// sequence of choice deltas.
//
// Everything here was checked against a live endpoint (OpenRouter's
// /api/v1/messages, which serves both Anthropic and non-Anthropic models) and
// several of the details below are ones the specification alone would not have
// given. They are marked where they appear.

const defaultAnthropicBase = "https://api.anthropic.com/v1"

// anthropicVersion is the API version header Anthropic requires. Gateways that
// speak the dialect accept it too, and Anthropic itself rejects a request
// without it.
const anthropicVersion = "2023-06-01"

// defaultMaxTokens is sent when the model config names no max_output.
// Anthropic requires max_tokens, so there is no "let the provider decide"
// option: a missing value is a 400, and guessing high is safer than guessing
// low, because a too-small cap truncates an answer mid-tool-call.
const defaultMaxTokens = 32768

// AnthropicClient speaks Anthropic Messages to one endpoint.
type AnthropicClient struct {
	Provider  config.Provider
	Transport http.RoundTripper

	// StreamIdleTimeout bounds the gap between bytes on a started stream.
	StreamIdleTimeout time.Duration
}

// idleTimeout is how long a started stream may go silent before it is failed.
func (c *AnthropicClient) idleTimeout() time.Duration {
	if c.StreamIdleTimeout > 0 {
		return c.StreamIdleTimeout
	}
	return defaultStreamIdleTimeout
}

// ForProvider builds the client for a provider's dialect. It is the one place
// that maps an adapter name onto a wire protocol, so a caller never has to
// know which dialect a provider speaks.
func ForProvider(p config.Provider) llm.ModelClient {
	switch p.Adapter {
	case config.AdapterAnthropic, config.AdapterOpenCodeAnthropic:
		return NewAnthropic(p)
	default:
		return New(p)
	}
}

// NewAnthropic builds a client for a provider endpoint.
func NewAnthropic(p config.Provider) *AnthropicClient {
	c := &AnthropicClient{Provider: p}
	if t, err := httpx.ProxyTransport(p.Proxy); err == nil {
		c.Transport = t
	}
	return c
}

func (c *AnthropicClient) baseURL() string {
	if c.Provider.BaseURL != "" {
		return strings.TrimRight(c.Provider.BaseURL, "/")
	}
	if c.Provider.Adapter == config.AdapterOpenCodeAnthropic {
		return defaultOpenCodeBase
	}
	return defaultAnthropicBase
}

// Wire shapes. Only the fields Strument reads are named: a gateway adds its
// own (OpenRouter sends caller, container, stop_details, provider, cost), and
// unknown fields must be ignored rather than rejected.

type antTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// antBlock is one block of message content. Only one of the payloads is set,
// selected by Type: "text", "tool_use" or "tool_result".
type antBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text,omitempty"`
	ID           string            `json:"id,omitempty"`          // tool_use
	Name         string            `json:"name,omitempty"`        // tool_use
	Input        json.RawMessage   `json:"input,omitempty"`       // tool_use
	ToolUseID    string            `json:"tool_use_id,omitempty"` // tool_result
	Content      string            `json:"content,omitempty"`     // tool_result
	CacheControl *llm.CacheControl `json:"cache_control,omitempty"`
}

type antMessage struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

// BuildBody translates an llm.Request into the Messages dialect.
func (c *AnthropicClient) BuildBody(req llm.Request) map[string]any {
	body := map[string]any{}
	maps.Copy(body, req.ExtraParams) // fenced passthrough, same rule as OpenAI's

	system, msgs := splitSystem(req.Messages)

	body["model"] = req.Model
	body["messages"] = msgs
	body["stream"] = true
	if len(system) > 0 {
		body["system"] = system
	}
	// Required, unlike chat-completions.
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	} else {
		body["max_tokens"] = defaultMaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := make([]antTool, len(req.Tools))
		for i, t := range req.Tools {
			// A tool with no parameters has a nil Parameters map, which
			// serializes as null. chat-completions tolerates that; Anthropic
			// requires input_schema to be an object and rejects the *whole
			// request* if any one tool's is null — so a single parameterless
			// tool (interrupt) 400s every send. An empty object is the
			// equivalent Anthropic accepts.
			schema := t.Parameters
			if len(schema) == 0 {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools[i] = antTool{Name: t.Name, Description: t.Description, InputSchema: schema}
		}
		body["tools"] = tools
		switch req.ToolChoice {
		case "auto":
			body["tool_choice"] = map[string]any{"type": "auto"}
		case "none":
			body["tool_choice"] = map[string]any{"type": "none"}
		}
	}
	return body
}

// splitSystem lifts system messages into the top-level field and rewrites the
// rest into Anthropic's shape. Three translations happen here:
//
//   - System is not a role. Every system message becomes a top-level block,
//     which is also where a cache breakpoint has to travel for the prompt
//     prefix to be cacheable at all.
//   - A tool result is not a role either: it is a tool_result block inside a
//     *user* message. Consecutive results merge into one message, because
//     Anthropic requires user and assistant turns to alternate and a parallel
//     tool call produces several results in a row.
//   - An assistant turn carries its tool calls as tool_use blocks alongside
//     its text, rather than in a separate field.
func splitSystem(in []llm.Message) ([]antBlock, []antMessage) {
	var system []antBlock
	var out []antMessage

	appendUser := func(b antBlock) {
		if n := len(out); n > 0 && out[n-1].Role == llm.RoleUser {
			out[n-1].Content = append(out[n-1].Content, b)
			return
		}
		out = append(out, antMessage{Role: llm.RoleUser, Content: []antBlock{b}})
	}

	for _, m := range in {
		switch m.Role {
		case llm.RoleSystem:
			system = append(system, contentBlocks(m.Content)...)
		case llm.RoleTool:
			appendUser(antBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Text(),
			})
		case llm.RoleAssistant:
			blocks := contentBlocks(m.Content)
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}" // an empty argument string is not valid JSON input
				}
				blocks = append(blocks, antBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: json.RawMessage(args),
				})
			}
			if len(blocks) == 0 {
				continue // an assistant turn with nothing in it is rejected
			}
			out = append(out, antMessage{Role: llm.RoleAssistant, Content: blocks})
		default:
			for _, b := range contentBlocks(m.Content) {
				appendUser(b)
			}
		}
	}
	return system, out
}

// contentBlocks renders llm.Content as Anthropic text blocks, carrying any
// cache breakpoint through — llm.CacheControl is already Anthropic's own
// shape, so it needs no translation.
func contentBlocks(c llm.Content) []antBlock {
	if c.Text != nil {
		if *c.Text == "" {
			return nil
		}
		return []antBlock{{Type: "text", Text: *c.Text}}
	}
	var out []antBlock
	for _, b := range c.Blocks {
		if b.Text == "" {
			continue
		}
		out = append(out, antBlock{Type: "text", Text: b.Text, CacheControl: b.CacheControl})
	}
	return out
}

// Stream event shapes.
type antStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`

	Message *struct {
		Usage antUsage `json:"usage"`
	} `json:"message"` // message_start

	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"` // content_block_start

	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"` // content_block_delta and message_delta

	Usage *antUsage `json:"usage"` // message_delta

	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type antUsage struct {
	InputTokens         int       `json:"input_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	CacheCreationTokens int       `json:"cache_creation_input_tokens"`
	CacheReadTokens     int       `json:"cache_read_input_tokens"`
	Cost                flexFloat `json:"cost"` // gateway extension; absent upstream
}

func (u antUsage) toLLM() llm.Usage {
	return llm.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		CacheWriteTokens: u.CacheCreationTokens,
		CacheReadTokens:  u.CacheReadTokens,
		Cost:             u.Cost.ptr(),
	}
}

// Send streams one response.
//
// The stream is a state machine over content blocks, and three things about it
// were learned from live captures rather than from the specification:
//
//   - A response has *several* blocks, and which ones depends on the model.
//     claude-haiku-4.5 answered a tool prompt with a single tool_use block;
//     xiaomi/mimo-v2.5, through the same endpoint, sent a thinking block at
//     index 0 and the tool_use at index 1. Fragments must therefore be
//     attributed by their block index, not accumulated globally — a client
//     written for the single-block case works against one model and silently
//     misassembles the other.
//   - Usage is not reported the same way twice. Haiku puts real input_tokens
//     on message_start; MiMo reports 0 there and the true count only on
//     message_delta. So message_delta is the authority and message_start is
//     only a fallback, or a cheap model looks free.
//   - OpenRouter appends a "data: [DONE]" sentinel that is not part of the
//     Anthropic protocol. It must be tolerated, not parsed.
func (c *AnthropicClient) Send(ctx context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		payload, err := json.Marshal(c.BuildBody(req))
		if err != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrServer, Message: "marshal request: " + err.Error()})
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/messages", bytes.NewReader(payload))
		if err != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrNetwork, Message: err.Error()})
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", userAgent)
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
		if c.Provider.APIKey != "" {
			// Both, because the gateways genuinely disagree and each ignores
			// the other's header. Measured: opencode's /messages rejects an
			// Authorization-only request with 401 "Missing API key." and
			// accepts x-api-key; OpenRouter's /messages accepts Authorization.
			// anthropic-version is optional at opencode and required by
			// Anthropic itself, so it goes on every request.
			httpReq.Header.Set("X-Api-Key", c.Provider.APIKey)
			httpReq.Header.Set("Authorization", "Bearer "+c.Provider.APIKey)
		}
		if c.Provider.Adapter == config.AdapterOpenCodeAnthropic {
			httpReq.Header.Set("X-Opencode-Session", sessionID())
		}

		httpClient := &http.Client{Transport: c.Transport}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				yield(llm.StreamEvent{}, ctx.Err())
				return
			}
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrNetwork, Message: err.Error()})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			yield(llm.StreamEvent{}, classifyHTTPError(resp, c.Provider.Adapter))
			return
		}

		// Same stall guard as the OpenAI path: a started stream that goes
		// silent must fail rather than hang, and a stream cut off before its
		// message_stop is a failure rather than a short answer.
		idle := c.idleTimeout()
		body := newIdleReader(resp.Body, idle)
		defer body.Close()

		stalled := func() bool {
			if body.Stalled() {
				yield(llm.StreamEvent{}, &llm.StreamError{
					Class:   llm.ErrNetwork,
					Message: fmt.Sprintf("the provider stopped sending for %s mid-response", idle),
				})
				return true
			}
			return false
		}
		if !c.parseStream(body, yield) {
			return
		}
		if !stalled() && ctx.Err() != nil {
			yield(llm.StreamEvent{}, ctx.Err())
		}
	}
}

// blockKind remembers what a content block index is, because the deltas that
// follow do not repeat it.
type blockKind struct {
	kind      string // "text" | "thinking" | "tool_use"
	toolIndex int    // position among tool calls, which is what llm.ToolCallDelta counts
}

// parseStream consumes the event stream. It returns false once the consumer
// has stopped or an error has been reported, so the caller does not then
// report a second reason for the same ending.
func (c *AnthropicClient) parseStream(body io.Reader, yield func(llm.StreamEvent, error) bool) bool {
	blocks := map[int]blockKind{}
	nextTool := 0
	var last llm.Usage
	sawUsage := false

	for data, err := range scanSSEData(body) {
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return false
		}

		var ev antStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // a frame we cannot read is not a reason to end the turn
		}
		if ev.Error != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{
				Class:   llm.ErrServer,
				Message: ev.Error.Type + ": " + ev.Error.Message,
			})
			return false
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				last, sawUsage = ev.Message.Usage.toLLM(), true
			}

		case "content_block_start":
			if ev.ContentBlock == nil {
				continue
			}
			b := blockKind{kind: ev.ContentBlock.Type}
			if b.kind == "tool_use" {
				b.toolIndex = nextTool
				nextTool++
				if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
					Index: b.toolIndex,
					ID:    ev.ContentBlock.ID,
					Name:  ev.ContentBlock.Name,
				}}, nil) {
					return false
				}
			}
			blocks[ev.Index] = b

		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			b := blocks[ev.Index]
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" && !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: ev.Delta.Text}, nil) {
					return false
				}
			case "thinking_delta":
				if ev.Delta.Thinking != "" && !yield(llm.StreamEvent{Kind: llm.EventReasoning, Text: ev.Delta.Thinking}, nil) {
					return false
				}
			case "input_json_delta":
				if ev.Delta.PartialJSON == "" {
					continue
				}
				if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
					Index: b.toolIndex,
					Args:  ev.Delta.PartialJSON,
				}}, nil) {
					return false
				}
			}

		case "message_delta":
			if ev.Usage != nil {
				// The authority: message_start can report zero.
				u := ev.Usage.toLLM()
				if u.PromptTokens == 0 && sawUsage {
					u.PromptTokens = last.PromptTokens
				}
				last, sawUsage = u, true
			}
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				if sawUsage {
					if !yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: &last}, nil) {
						return false
					}
					sawUsage = false
				}
				if !yield(llm.StreamEvent{
					Kind:         llm.EventFinish,
					FinishReason: finishReasonFromAnthropic(ev.Delta.StopReason),
				}, nil) {
					return false
				}
			}

		case "message_stop":
			if sawUsage {
				yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: &last}, nil)
				sawUsage = false
			}
		}
	}
	return true
}

// finishReasonFromAnthropic maps stop_reason onto the chat-completions names
// the coder already branches on, so one set of rules covers both dialects.
func finishReasonFromAnthropic(stop string) string {
	switch stop {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "end_turn", "stop_sequence":
		return "stop"
	default:
		return stop
	}
}

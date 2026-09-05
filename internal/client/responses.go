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

// OpenAI's Responses API. The third dialect, and the least like the other two:
// there are no "messages", only a flat list of input *items* where a tool call
// and its result are items in their own right rather than fields on a message,
// the system prompt is `instructions`, and the stream reports item lifecycles
// rather than content deltas alone.
//
// As with the Anthropic client, the shapes here were read off live captures
// (OpenRouter's /api/v1/responses); the ones a specification alone would not
// have settled are marked where they appear.

const defaultResponsesBase = "https://api.openai.com/v1"

// ResponsesClient speaks the Responses API to one endpoint.
type ResponsesClient struct {
	Provider  config.Provider
	Transport http.RoundTripper

	// StreamIdleTimeout bounds the gap between bytes on a started stream.
	StreamIdleTimeout time.Duration
}

// NewResponses builds a client for a provider endpoint.
func NewResponses(p config.Provider) *ResponsesClient {
	c := &ResponsesClient{Provider: p}
	if t, err := httpx.ProxyTransport(p.Proxy); err == nil {
		c.Transport = t
	}
	return c
}

func (c *ResponsesClient) idleTimeout() time.Duration {
	if c.StreamIdleTimeout > 0 {
		return c.StreamIdleTimeout
	}
	return defaultStreamIdleTimeout
}

func (c *ResponsesClient) baseURL() string {
	if c.Provider.BaseURL != "" {
		return strings.TrimRight(c.Provider.BaseURL, "/")
	}
	if c.Provider.Adapter == config.AdapterOpenCodeResponses {
		return defaultOpenCodeBase
	}
	return defaultResponsesBase
}

// respItem is one element of the input list. A message carries role and
// content; a function_call and a function_call_output are items of their own,
// paired by call_id.
type respItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

// BuildBody translates an llm.Request into the Responses dialect.
func (c *ResponsesClient) BuildBody(req llm.Request) map[string]any {
	body := map[string]any{}
	maps.Copy(body, req.ExtraParams)

	instructions, items := splitInstructions(req.Messages)

	body["model"] = req.Model
	body["input"] = items
	body["stream"] = true
	// Strument keeps the whole conversation and resends it, so server-side
	// storage would be a second copy of the same history with its own
	// lifetime — a source of truth the user cannot see, edit or undo. Off also
	// means nothing of the session outlives the request.
	body["store"] = false
	if instructions != "" {
		body["instructions"] = instructions
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	// Reasoning. "" and "default" send nothing at all, as in the other two
	// dialects — and here that is load bearing rather than tidy. A reasoning
	// object carrying only {"summary":"auto"} is read by some providers as
	// *disabling* reasoning: x-ai/grok-4.6 rejects it outright with
	// "Reasoning is mandatory for this endpoint and cannot be disabled",
	// while openai/gpt-5.6-luna accepts the same body. Omitting the key is
	// accepted by both.
	//
	// The consequence is worth stating: without an effort there is no summary,
	// and a summary is the only form in which reasoning is ever visible on
	// this API — the raw item carries an opaque encrypted_content blob. So a
	// model configured with no `reasoning` shows no thinking here, and one
	// with any effect set shows it. That is the same "defer to the provider"
	// the other dialects mean, and the provider's own default is silence.
	//
	// There is no way to turn reasoning off — it is what these models are — so
	// "off" asks for the least of it and no summary, which is the closest this
	// API comes to the other dialects' meaning: nothing on screen.
	switch effort := req.ReasoningEffort; effort {
	case "", "default":
		// Send nothing.
	case "off":
		body["reasoning"] = map[string]any{"effort": "low"}
	default:
		body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
	}

	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				// Same trap as the other two dialects: a nil map marshals as
				// null and a strict schema check rejects it.
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			// Flat, unlike chat-completions: name and parameters sit on the
			// tool itself rather than under a nested "function" object.
			tools[i] = map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			}
		}
		body["tools"] = tools
		if req.ToolChoice != "" {
			body["tool_choice"] = req.ToolChoice
		}
	}
	return body
}

// splitInstructions lifts system messages into `instructions` and flattens the
// rest into input items. An assistant turn's tool calls become sibling
// function_call items rather than a field on the message, and a tool result
// becomes a function_call_output item — so the pairing is by call_id and the
// order of the list is the whole of the structure.
func splitInstructions(in []llm.Message) (string, []respItem) {
	var instructions []string
	var items []respItem

	for _, m := range in {
		switch m.Role {
		case llm.RoleSystem:
			if t := m.Text(); t != "" {
				instructions = append(instructions, t)
			}
		case llm.RoleTool:
			items = append(items, respItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Text(),
			})
		case llm.RoleAssistant:
			if t := m.Text(); t != "" {
				items = append(items, respItem{Role: llm.RoleAssistant, Content: t})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				items = append(items, respItem{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: args,
				})
			}
		default:
			if t := m.Text(); t != "" {
				items = append(items, respItem{Role: llm.RoleUser, Content: t})
			}
		}
	}
	return strings.Join(instructions, "\n\n"), items
}

// respStreamEvent is one server-sent event. The Responses stream reports item
// lifecycles as well as content, so an event is identified by `type` and
// scoped by `output_index` — the position of the item it belongs to.
type respStreamEvent struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	Delta       string `json:"delta"`

	Item *struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`

	Response *struct {
		Status string `json:"status"`
		Usage  *struct {
			InputTokens        int       `json:"input_tokens"`
			OutputTokens       int       `json:"output_tokens"`
			Cost               flexFloat `json:"cost"`
			InputTokensDetails *struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`

	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Send streams one response.
//
// Two things here came from the captures rather than the shape of the API:
//
//   - output_index is the index of the *item*, not of the tool call. A reply
//     that reasons before calling tools puts the reasoning at index 0 and the
//     calls at 1 and 2, so tool calls have to be numbered separately or the
//     coder sees a call at index 1 with nothing at index 0.
//   - Reasoning is only ever visible as a summary, and only if one is asked
//     for. Without `summary: "auto"` the reasoning item arrives with an empty
//     summary and an opaque encrypted_content blob, and the panel that renders
//     thinking shows nothing at all.
func (c *ResponsesClient) Send(ctx context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		payload, err := json.Marshal(c.BuildBody(req))
		if err != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrServer, Message: "marshal request: " + err.Error()})
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/responses", bytes.NewReader(payload))
		if err != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrNetwork, Message: err.Error()})
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", userAgent)
		if c.Provider.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.Provider.APIKey)
		}
		if c.Provider.Adapter == config.AdapterOpenCodeResponses {
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

		idle := c.idleTimeout()
		body := newIdleReader(resp.Body, idle)
		defer body.Close()

		if !c.parseStream(body, yield) {
			return
		}
		if body.Stalled() {
			yield(llm.StreamEvent{}, &llm.StreamError{
				Class:   llm.ErrNetwork,
				Message: fmt.Sprintf("the provider stopped sending for %s mid-response", idle),
			})
			return
		}
		if ctx.Err() != nil {
			yield(llm.StreamEvent{}, ctx.Err())
		}
	}
}

func (c *ResponsesClient) parseStream(body io.Reader, yield func(llm.StreamEvent, error) bool) bool {
	// output_index -> position among tool calls. Items that are not calls
	// (reasoning, messages) never enter it, which is what keeps the tool
	// indices contiguous from zero.
	toolIndex := map[int]int{}
	nextTool := 0

	for data, err := range scanSSEData(body) {
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return false
		}

		var ev respStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if ev.Error != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{
				Class:   llm.ErrServer,
				Message: ev.Error.Type + ": " + ev.Error.Message,
			})
			return false
		}

		switch ev.Type {
		case "response.output_item.added":
			if ev.Item == nil || ev.Item.Type != "function_call" {
				continue
			}
			idx := nextTool
			nextTool++
			toolIndex[ev.OutputIndex] = idx
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: idx,
				ID:    ev.Item.CallID,
				Name:  ev.Item.Name,
			}}, nil) {
				return false
			}

		case "response.function_call_arguments.delta":
			if ev.Delta == "" {
				continue
			}
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: toolIndex[ev.OutputIndex],
				Args:  ev.Delta,
			}}, nil) {
				return false
			}

		case "response.output_text.delta":
			if ev.Delta != "" && !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: ev.Delta}, nil) {
				return false
			}

		case "response.reasoning_summary_text.delta":
			if ev.Delta != "" && !yield(llm.StreamEvent{Kind: llm.EventReasoning, Text: ev.Delta}, nil) {
				return false
			}

		case "response.completed", "response.incomplete", "response.failed":
			if ev.Response == nil {
				continue
			}
			if u := ev.Response.Usage; u != nil {
				usage := llm.Usage{
					PromptTokens:     u.InputTokens,
					CompletionTokens: u.OutputTokens,
					Cost:             u.Cost.ptr(),
				}
				if d := u.InputTokensDetails; d != nil {
					usage.CacheReadTokens = d.CachedTokens
					usage.CacheWriteTokens = d.CacheWriteTokens
				}
				if !yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: &usage}, nil) {
					return false
				}
			}
			if !yield(llm.StreamEvent{
				Kind:         llm.EventFinish,
				FinishReason: finishReasonFromResponses(ev),
			}, nil) {
				return false
			}
		}
	}
	return true
}

// finishReasonFromResponses maps a terminal event onto the chat-completions
// names the coder already branches on. A tool call is not announced as a
// status here — the response simply completes with call items in it — so
// "tool_calls" is inferred by the coder from the calls it received, and this
// reports only what the API itself distinguishes.
func finishReasonFromResponses(ev respStreamEvent) string {
	if ev.Type == "response.failed" {
		return "error"
	}
	if ev.Response != nil && ev.Response.IncompleteDetails != nil {
		if ev.Response.IncompleteDetails.Reason == "max_output_tokens" {
			return "length"
		}
		return ev.Response.IncompleteDetails.Reason
	}
	return "stop"
}

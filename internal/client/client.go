// Package client is Strument's single OpenAI-compatible chat client,
// speaking OpenRouter's dialect where the adapter says so. One Client
// serves one endpoint; the runtime groups
// models by (adapter, base_url).
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

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/llm"
)

// Default endpoints per adapter.
const (
	defaultOpenAIBase     = "https://api.openai.com/v1"
	defaultOpenRouterBase = "https://openrouter.ai/api/v1"
)

// OpenRouter app-attribution headers, so requests show as this app in the
// provider's logs and rankings instead of "Unknown".
const (
	appName = "Strument"
	appURL  = "https://dbohdan.com/strument"
)

// Client speaks to one endpoint. Transport may be overridden for tests —
// the automated suite never opens a socket.
type Client struct {
	Provider  config.Provider
	Transport http.RoundTripper
}

// New builds a client for a provider endpoint. A resolved provider proxy
// becomes the transport; the URL was validated at config load, so the error is
// dead here and a nil transport (no proxy) falls back to the default.
func New(p config.Provider) *Client {
	c := &Client{Provider: p}
	if t, err := httpx.ProxyTransport(p.Proxy); err == nil {
		c.Transport = t
	}
	return c
}

func (c *Client) baseURL() string {
	if c.Provider.BaseURL != "" {
		return strings.TrimRight(c.Provider.BaseURL, "/")
	}
	if c.Provider.Adapter == config.AdapterOpenRouter {
		return defaultOpenRouterBase
	}
	return defaultOpenAIBase
}

// wireMessage is one message on the wire.
type wireMessage struct {
	Role       string         `json:"role"`
	Content    llm.Content    `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// wireToolCall is the OpenAI tool-call shape on an assistant message.
type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// BuildBody translates an llm.Request into the provider dialect
// (request-side reasoning effort differs between adapters; extra_params
// pass through beneath the transport keys Strument owns).
func (c *Client) BuildBody(req llm.Request) map[string]any {
	body := map[string]any{}

	// Fenced passthrough first: reserved keys were rejected at config
	// load, and writing ours afterwards keeps ownership regardless.
	maps.Copy(body, req.ExtraParams)

	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		wm := wireMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: wireToolFunction{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		msgs[i] = wm
	}
	body["model"] = req.Model
	body["messages"] = msgs
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	// Reasoning control. "" and "default" defer to the provider's own
	// default (send nothing). "off" turns reasoning off where the provider
	// can express it — OpenRouter via reasoning:{enabled:false}, Ollama-style
	// OpenAI endpoints via reasoning_effort:"none". Any other value is an
	// uninterpreted effort passed straight through, so a newly minted effort
	// works without a change here.
	switch effort := req.ReasoningEffort; {
	case effort == "" || effort == "default":
		// Defer to the provider default; send nothing.
	case c.Provider.Adapter == config.AdapterOpenRouter:
		if effort == "off" {
			body["reasoning"] = map[string]any{"enabled": false}
		} else {
			body["reasoning"] = map[string]any{"effort": effort}
		}
	default:
		if effort == "off" {
			body["reasoning_effort"] = "none"
		} else {
			body["reasoning_effort"] = effort
		}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			}
		}
		body["tools"] = tools
		if req.ToolChoice != "" {
			body["tool_choice"] = req.ToolChoice
		}
	}
	return body
}

// Send implements llm.ModelClient: one streaming request, events in wire
// order (Reasoning/Answer chunks, Finish, then Usage — OpenRouter's final
// chunk carries usage with in-band cost). Retry and continuation live in
// the coder, not here.
func (c *Client) Send(ctx context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		payload, err := json.Marshal(c.BuildBody(req))
		if err != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrServer, Message: "marshal request: " + err.Error()})
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrNetwork, Message: err.Error()})
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.Provider.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.Provider.APIKey)
		}
		if c.Provider.Adapter == config.AdapterOpenRouter {
			// OpenRouter's app-attribution headers. Its docs write
			// "HTTP-Referer"; Go canonicalizes it to "Http-Referer" and
			// header names are case-insensitive (RFC 9110), so it matches.
			httpReq.Header.Set("Http-Referer", appURL)
			httpReq.Header.Set("X-Title", appName)
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
			yield(llm.StreamEvent{}, classifyHTTPError(resp))
			return
		}

		for ev, err := range ParseSSE(resp.Body) {
			if err != nil {
				if ctx.Err() != nil {
					yield(llm.StreamEvent{}, ctx.Err())
					return
				}
				yield(llm.StreamEvent{}, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// classifyHTTPError maps a non-200 response onto the error classes.
func classifyHTTPError(resp *http.Response) *llm.StreamError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	msg := extractErrorMessage(body)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		msg = resp.Status
	}

	// A 4xx other than the two transient ones is the request being wrong, and
	// the provider will say the same thing next time. Classifying the whole
	// 4xx range as ErrServer made a typo'd model slug cost the full retry
	// ladder — about a minute of "Retrying in 0.2 seconds..." before the one
	// line that explains it ("nope/does-not-exist is not a valid model ID").
	class := llm.ErrServer
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		class = llm.ErrAuth
	case resp.StatusCode == http.StatusTooManyRequests:
		class = llm.ErrRateLimit
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout:
		class = llm.ErrServer
	case resp.StatusCode >= 400:
		if isContextWindowMessage(msg) {
			class = llm.ErrContextWindow
		} else {
			class = llm.ErrRequest
		}
	}
	return &llm.StreamError{Class: class, Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg)}
}

// isContextWindowMessage spots provider phrasing for over-long prompts.
func isContextWindowMessage(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "context length") ||
		strings.Contains(m, "context window") ||
		strings.Contains(m, "maximum context") ||
		strings.Contains(m, "too many tokens") ||
		strings.Contains(m, "prompt is too long")
}

func extractErrorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return ""
}

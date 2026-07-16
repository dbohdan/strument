// Package client is Strument's single OpenAI-compatible chat client,
// speaking OpenRouter's dialect where the adapter says so (basecoder-spec
// §8; config-schema §3). One Client serves one endpoint; the runtime groups
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

	"github.com/dbohdan/strument/internal/config"
	"github.com/dbohdan/strument/internal/llm"
)

// Default endpoints per adapter.
const (
	defaultOpenAIBase     = "https://api.openai.com/v1"
	defaultOpenRouterBase = "https://openrouter.ai/api/v1"
)

// Client speaks to one endpoint. Transport may be overridden for tests —
// the automated suite never opens a socket.
type Client struct {
	Provider  config.Provider
	Transport http.RoundTripper
}

// New builds a client for a provider endpoint.
func New(p config.Provider) *Client {
	return &Client{Provider: p}
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
	Role    string      `json:"role"`
	Content llm.Content `json:"content"`
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
		msgs[i] = wireMessage{Role: m.Role, Content: m.Content}
	}
	body["model"] = req.Model
	body["messages"] = msgs
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.ReasoningEffort != "" {
		switch c.Provider.Adapter {
		case config.AdapterOpenRouter:
			body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
		default:
			body["reasoning_effort"] = req.ReasoningEffort
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

// classifyHTTPError maps a non-200 response onto the §2.1 error classes.
func classifyHTTPError(resp *http.Response) *llm.StreamError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	msg := extractErrorMessage(body)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		msg = resp.Status
	}

	class := llm.ErrServer
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		class = llm.ErrAuth
	case resp.StatusCode == http.StatusTooManyRequests:
		class = llm.ErrRateLimit
	case resp.StatusCode >= 500:
		class = llm.ErrServer
	case resp.StatusCode >= 400:
		if isContextWindowMessage(msg) {
			class = llm.ErrContextWindow
		} else {
			class = llm.ErrServer
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

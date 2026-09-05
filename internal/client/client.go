// Package client is Strument's single OpenAI-compatible chat client,
// speaking OpenRouter's dialect where the adapter says so. One Client
// serves one endpoint; the runtime groups
// models by (adapter, base_url).
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/llm"
)

// Default endpoints per adapter.
const (
	defaultOpenAIBase     = "https://api.openai.com/v1"
	defaultOpenRouterBase = "https://openrouter.ai/api/v1"
	// opencode Go splits its catalogue across three protocols: /messages for
	// the MiniMax and Qwen3.6-3.8 models, /responses for Grok 4.6, GPT-5.6
	// Luna and Muse Spark, and /chat/completions for the rest. Strument speaks
	// only the last, so this adapter reaches the GLM, Kimi, DeepSeek, MiMo,
	// LongCat, Hy and Omen models and not the other twelve.
	defaultOpenCodeBase = "https://opencode.ai/zen/go/v1"
)

// OpenRouter app-attribution headers, so requests show as this app in the
// provider's logs and rankings instead of "Unknown".
const (
	appName = "Strument"
	appURL  = "https://dbohdan.com/strument"
)

// userAgent identifies Strument on every request. Without it Go sends
// "Go-http-client/2.0", which is exactly the broad user agent some providers
// ask callers not to send — opencode Go says so in as many words, and treats
// it as grounds for flagging an account. Set once from main before any request
// goes out; the zero value is still a real name, so a caller that forgets is
// merely unversioned rather than anonymous.
var userAgent = appName

// SetVersion fixes the User-Agent for the process. Call it once at startup.
func SetVersion(v string) {
	if v == "" {
		userAgent = appName
		return
	}
	userAgent = appName + "/" + v + " (+" + appURL + ")"
}

// Client speaks to one endpoint. Transport may be overridden for tests —
// the automated suite never opens a socket.
type Client struct {
	Provider  config.Provider
	Transport http.RoundTripper

	// StreamIdleTimeout bounds the gap between bytes on a started stream.
	// Zero uses defaultStreamIdleTimeout. Tests set it small.
	StreamIdleTimeout time.Duration
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
	switch c.Provider.Adapter {
	case config.AdapterOpenRouter:
		return defaultOpenRouterBase
	case config.AdapterOpenCode:
		return defaultOpenCodeBase
	default:
		return defaultOpenAIBase
	}
}

// sessionID is one opaque id for the life of the process, sent to opencode Go
// so it can group a session's requests and keep the prompt cache warm across
// them. That is worth real money there rather than being a courtesy: their own
// usage estimates are dominated by cached tokens (MiMo-V2.5: 830 input against
// 71,500 cached per request), so a session that never gets a cache hit spends
// its allowance many times faster.
//
// Random per process, and it identifies nothing about the machine or the user:
// grouping is the whole of what it is for. It is computed once and only when an
// opencode provider actually sends, so no other adapter's traffic carries one.
var sessionID = sync.OnceValue(func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; if it ever did, a
		// less-unique id still groups this process's requests correctly.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
})

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
			params := t.Parameters
			if params == nil {
				// A parameterless tool has a nil Parameters map, which marshals
				// as null. Strict-schema providers (e.g. GPT-5.6 Luna) reject
				// that with "expected object, received null", so send an empty
				// schema object instead.
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  params,
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
		httpReq.Header.Set("User-Agent", userAgent)
		if c.Provider.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.Provider.APIKey)
		}
		if c.Provider.Adapter == config.AdapterOpenCode {
			// opencode Go asks callers to send this so it can optimize prompt
			// caching, and treats its absence as a reason to flag an account.
			// Their docs write "x-opencode-session"; Go canonicalizes it and
			// header names are case-insensitive (RFC 9110), so it matches.
			httpReq.Header.Set("X-Opencode-Session", sessionID())
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
			yield(llm.StreamEvent{}, classifyHTTPError(resp, c.Provider.Adapter))
			return
		}

		// A started stream that goes silent used to hang here forever: there is
		// no client timeout, no context deadline on a send, and a bare scan over
		// the body. See idle.go.
		idle := c.idleTimeout()
		body := newIdleReader(resp.Body, idle)
		defer body.Close()

		for ev, err := range ParseSSE(body) {
			if err != nil {
				if body.Stalled() {
					yield(llm.StreamEvent{}, &llm.StreamError{
						Class: llm.ErrNetwork,
						Message: fmt.Sprintf("the provider stopped sending for %s "+
							"mid-response", idle),
					})
					return
				}
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
		// A closed body ends the scan without an error, so the stall has to be
		// reported here as well as inside the loop: a stream cut off before
		// [DONE] is a failure, not a short answer.
		if body.Stalled() {
			yield(llm.StreamEvent{}, &llm.StreamError{
				Class:   llm.ErrNetwork,
				Message: fmt.Sprintf("the provider stopped sending for %s mid-response", idle),
			})
		}
	}
}

// idleTimeout is how long a started stream may go silent before it is failed.
// Zero means the default; a provider can raise it for a slow endpoint.
func (c *Client) idleTimeout() time.Duration {
	if c.StreamIdleTimeout > 0 {
		return c.StreamIdleTimeout
	}
	return defaultStreamIdleTimeout
}

// classifyHTTPError maps a non-200 response onto the error classes.
// opencodeRoutingHint is appended when an opencode request fails in one of the
// ways a slug on the wrong adapter fails.
//
// opencode Go serves three protocols and enforces the split in both
// directions, but says so inconsistently: measured against the live endpoint,
// grok-4.6 on /chat/completions answers 401, gpt-5.6-luna on the same endpoint
// answers 500, and mimo-v2.5 on /messages answers 500 — the same mistake, three
// codes, none of which mentions endpoints. A 401 in particular reads as "your
// key is wrong", which sends the user to rotate a key that was fine.
//
// The map from model to protocol is not discoverable — /v1/models lists ids
// and nothing else — so this is a mistake a user cannot avoid by reading, and
// the hint is worth more than its noise. It is phrased as a possibility
// because these codes really are ambiguous: a 401 can still be a bad key.
func opencodeRoutingHint(adapter string, status int) string {
	if status != http.StatusUnauthorized && status < 500 {
		return ""
	}
	switch adapter {
	case config.AdapterOpenCode:
		return "\nIf the key is good, this model may be one opencode serves over " +
			"/messages or /responses rather than /chat/completions — try the " +
			"\"opencode-anthropic\" or \"opencode-responses\" adapter. opencode's model table says which."
	case config.AdapterOpenCodeAnthropic:
		return "\nIf the key is good, this model may be one opencode serves over " +
			"/chat/completions or /responses rather than /messages — try the " +
			"\"opencode\" or \"opencode-responses\" adapter. opencode's model table says which."
	case config.AdapterOpenCodeResponses:
		return "\nIf the key is good, this model may be one opencode serves over " +
			"/chat/completions or /messages rather than /responses — try the " +
			"\"opencode\" or \"opencode-anthropic\" adapter. opencode's model table says which."
	}
	return ""
}

func classifyHTTPError(resp *http.Response, adapter string) *llm.StreamError {
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
	msg += opencodeRoutingHint(adapter, resp.StatusCode)
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

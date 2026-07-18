package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

// The production SSE parser must produce exactly the event sequence that the
// distilled scenario fixture recorded from the same capture — the parser is
// the single source of dialect truth.
func TestParseSSEMatchesDistilledFixture(t *testing.T) {
	raw, err := os.Open("../../testdata/fixtures/client/edit-success.sse")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	sc, err := fixture.Load("../../testdata/fixtures/basecoder/edit-success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	want := sc.Turns[0].Events

	var got []llm.StreamEvent
	for ev, err := range ParseSSE(raw) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}

	if len(got) != len(want) {
		t.Fatalf("event count: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		w := want[i]
		g := got[i]
		if string(g.Kind) != w.Kind || g.Text != w.Text || g.FinishReason != w.FinishReason {
			t.Fatalf("event %d: got %+v, want %+v", i, g, w)
		}
		if w.Kind == "Usage" {
			if g.Usage == nil || g.Usage.PromptTokens != w.Usage.PromptTokens ||
				g.Usage.CompletionTokens != w.Usage.CompletionTokens {
				t.Fatalf("usage mismatch: got %+v, want %+v", g.Usage, w.Usage)
			}
			if g.Usage.Cost == nil || w.Usage.Cost == nil || *g.Usage.Cost != *w.Usage.Cost {
				t.Fatalf("cost mismatch: got %v, want %v", g.Usage.Cost, w.Usage.Cost)
			}
		}
	}
}

func TestBuildBodyDialects(t *testing.T) {
	req := llm.Request{
		Model:           "deepseek/deepseek-v4-flash",
		Messages:        []llm.Message{llm.TextMessage("user", "hi")},
		ReasoningEffort: "low",
		ExtraParams:     map[string]any{"service_tier": "default", "temperature": nil},
	}

	or := New(config.Provider{Adapter: config.AdapterOpenRouter})
	body := or.BuildBody(req)
	if r, ok := body["reasoning"].(map[string]any); !ok || r["effort"] != "low" {
		t.Errorf("openrouter reasoning = %v", body["reasoning"])
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Error("openrouter body must not use reasoning_effort")
	}
	if body["service_tier"] != "default" {
		t.Errorf("extra param lost: %v", body["service_tier"])
	}
	if body["stream"] != true {
		t.Error("stream must be true")
	}
	if body["model"] != "deepseek/deepseek-v4-flash" {
		t.Errorf("model = %v", body["model"])
	}

	oa := New(config.Provider{Adapter: config.AdapterOpenAI})
	body = oa.BuildBody(req)
	if body["reasoning_effort"] != "low" {
		t.Errorf("openai reasoning_effort = %v", body["reasoning_effort"])
	}
	if _, ok := body["reasoning"]; ok {
		t.Error("openai body must not use the reasoning object")
	}

	// Owned transport keys are written after passthrough: an extra_params
	// "temperature" collision (nil above) can't clobber the real value.
	temp := 0.5
	req.Temperature = &temp
	body = oa.BuildBody(req)
	if body["temperature"] != 0.5 {
		t.Errorf("temperature = %v", body["temperature"])
	}
}

func TestMessageSerialization(t *testing.T) {
	blocks := llm.Content{Blocks: []llm.ContentBlock{
		{Type: "text", Text: "cached prefix", CacheControl: &llm.CacheControl{Type: "ephemeral"}},
	}}
	c := New(config.Provider{Adapter: config.AdapterOpenRouter})
	body := c.BuildBody(llm.Request{
		Model: "m",
		Messages: []llm.Message{
			llm.TextMessage("system", "sys"),
			{Role: "user", Content: blocks},
		},
	})
	data, err := json.Marshal(body["messages"])
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"role":"system","content":"sys"},{"role":"user","content":[{"type":"text","text":"cached prefix","cache_control":{"type":"ephemeral"}}]}]`
	if string(data) != want {
		t.Errorf("messages = %s", data)
	}
}

// roundTripFunc stubs the transport; the suite never opens a socket.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSendStreamsEvents(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"reasoning":"thinking"}}]}

data: {"choices":[{"delta":{"content":"hello"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"cost":0.001}}

data: [DONE]
`
	var gotReq *http.Request
	var gotBody []byte
	c := New(config.Provider{Adapter: config.AdapterOpenRouter, APIKey: "test-key"})
	c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotReq = r
		gotBody, _ = io.ReadAll(r.Body)
		return respond(200, "text/event-stream", sse), nil
	})

	var kinds []string
	var answer string
	var answerSb165 strings.Builder
	for ev, err := range c.Send(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.TextMessage("user", "hi")}}) {
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, string(ev.Kind))
		if ev.Kind == llm.EventAnswer {
			answerSb165.WriteString(ev.Text)
		}
	}
	answer += answerSb165.String()
	if strings.Join(kinds, ",") != "Reasoning,Answer,Finish,Usage" {
		t.Errorf("kinds = %v", kinds)
	}
	if answer != "hello" {
		t.Errorf("answer = %q", answer)
	}
	if gotReq.URL.String() != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("url = %s", gotReq.URL)
	}
	if gotReq.Header.Get("Authorization") != "Bearer test-key" {
		t.Errorf("auth header = %q", gotReq.Header.Get("Authorization"))
	}
	var parsed map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil || parsed["stream"] != true {
		t.Errorf("request body: %s", gotBody)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   llm.ErrorClass
		retry  bool
	}{
		{401, `{"error":{"message":"bad key"}}`, llm.ErrAuth, false},
		{429, `{"error":{"message":"slow down"}}`, llm.ErrRateLimit, true},
		{500, `{"error":{"message":"boom"}}`, llm.ErrServer, true},
		{400, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, llm.ErrContextWindow, false},
		{400, `{"error":{"message":"invalid request"}}`, llm.ErrServer, true},
	}
	for _, tc := range cases {
		c := New(config.Provider{Adapter: config.AdapterOpenRouter, APIKey: "k"})
		c.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return respond(tc.status, "application/json", tc.body), nil
		})
		var gotErr error
		for _, err := range c.Send(context.Background(), llm.Request{Model: "m"}) {
			gotErr = err
			break
		}
		se := &llm.StreamError{}
		ok := errors.As(gotErr, &se)
		if !ok || se.Class != tc.want {
			t.Errorf("status %d: err = %v (want class %s)", tc.status, gotErr, tc.want)
			continue
		}
		if se.Retryable() != tc.retry {
			t.Errorf("status %d: retryable = %v, want %v", tc.status, se.Retryable(), tc.retry)
		}
	}
}

func TestMidStreamErrorObject(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"content":"partial"}}]}

data: {"error":{"message":"upstream failed","code":502}}
`
	var events []llm.StreamEvent
	var gotErr error
	for ev, err := range ParseSSE(strings.NewReader(sse)) {
		if err != nil {
			gotErr = err
			break
		}
		events = append(events, ev)
	}
	if len(events) != 1 || events[0].Text != "partial" {
		t.Errorf("events = %+v", events)
	}
	var se *llm.StreamError
	if !errors.As(gotErr, &se) || se.Class != llm.ErrServer {
		t.Errorf("err = %v", gotErr)
	}
}

type failingReader struct {
	data string
	read bool
}

func (f *failingReader) Read(p []byte) (int, error) {
	if !f.read {
		f.read = true
		return copy(p, f.data), nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestMidStreamConnectionDrop(t *testing.T) {
	fr := &failingReader{data: `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n"}
	var events []llm.StreamEvent
	var gotErr error
	for ev, err := range ParseSSE(fr) {
		if err != nil {
			gotErr = err
			break
		}
		events = append(events, ev)
	}
	if len(events) != 1 {
		t.Errorf("events = %+v", events)
	}
	var se *llm.StreamError
	if !errors.As(gotErr, &se) || se.Class != llm.ErrNetwork || !se.Retryable() {
		t.Errorf("err = %v", gotErr)
	}
}

func TestOpenAIDefaultBase(t *testing.T) {
	c := New(config.Provider{Adapter: config.AdapterOpenAI})
	if got := c.baseURL(); got != "https://api.openai.com/v1" {
		t.Errorf("base = %s", got)
	}
	c2 := New(config.Provider{Adapter: config.AdapterOpenRouter, BaseURL: "https://proxy.corp/v1/"})
	if got := c2.baseURL(); got != "https://proxy.corp/v1" {
		t.Errorf("base = %s", got)
	}
}

func TestOpenRouterAppHeaders(t *testing.T) {
	for _, tc := range []struct {
		adapter string
		wantApp bool
	}{
		{config.AdapterOpenRouter, true},
		{config.AdapterOpenAI, false},
	} {
		var gotReq *http.Request
		c := New(config.Provider{Adapter: tc.adapter, APIKey: "k"})
		c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotReq = r
			return respond(200, "text/event-stream", "data: [DONE]\n"), nil
		})
		for _, err := range c.Send(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{llm.TextMessage("user", "hi")}}) {
			if err != nil {
				t.Fatal(err)
			}
		}
		title := gotReq.Header.Get("X-Title")
		referer := gotReq.Header.Get("Http-Referer")
		if tc.wantApp {
			if title != "Strument" {
				t.Errorf("%s: X-Title = %q, want Strument", tc.adapter, title)
			}
			if referer == "" {
				t.Errorf("%s: HTTP-Referer should be set", tc.adapter)
			}
		} else if title != "" || referer != "" {
			t.Errorf("%s: app headers should be absent, got X-Title=%q HTTP-Referer=%q", tc.adapter, title, referer)
		}
	}
}

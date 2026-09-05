package client

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

func antBody(t *testing.T, req llm.Request) map[string]any {
	t.Helper()
	c := NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic, APIKey: "k"})
	raw, err := json.Marshal(c.BuildBody(req))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// blocksOf returns the content blocks of the nth message in a built body.
func blocksOf(t *testing.T, body map[string]any, n int) []any {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	if n >= len(msgs) {
		t.Fatalf("wanted message %d, body has %d", n, len(msgs))
	}
	m, _ := msgs[n].(map[string]any)
	blocks, _ := m["content"].([]any)
	return blocks
}

func roleList(t *testing.T, body map[string]any) []string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i], _ = m.(map[string]any)["role"].(string)
	}
	return roles
}

// System is not a role in this dialect. Left in messages the request is
// rejected; dropped, the model loses its instructions silently, which is the
// worse of the two failures.
func TestAnthropicLiftsTheSystemPrompt(t *testing.T) {
	body := antBody(t, llm.Request{
		Model: "m",
		Messages: []llm.Message{
			llm.TextMessage(llm.RoleSystem, "be terse"),
			llm.TextMessage(llm.RoleUser, "hi"),
		},
	})
	sys, ok := body["system"].([]any)
	if !ok || len(sys) != 1 {
		t.Fatalf("system = %#v, want one block", body["system"])
	}
	sysBlock, _ := sys[0].(map[string]any)
	if got, _ := sysBlock["text"].(string); got != "be terse" {
		t.Errorf("system text = %q, want %q", got, "be terse")
	}
	if got := roleList(t, body); len(got) != 1 || got[0] != llm.RoleUser {
		t.Errorf("messages roles = %v, want just the user turn", got)
	}
}

// A tool result is a block inside a *user* message, and a parallel tool call
// produces several in a row. Anthropic requires user and assistant turns to
// alternate, so consecutive results have to merge into one turn rather than
// become three user messages the endpoint will refuse.
func TestAnthropicMergesToolResultsIntoOneTurn(t *testing.T) {
	body := antBody(t, llm.Request{
		Model: "m",
		Messages: []llm.Message{
			llm.TextMessage(llm.RoleUser, "read two files"),
			{Role: llm.RoleAssistant, Content: llm.TextContent(""), ToolCalls: []llm.ToolCall{
				{ID: "a", Name: "read", Arguments: `{"path":"1"}`},
				{ID: "b", Name: "read", Arguments: `{"path":"2"}`},
			}},
			llm.ToolResult("a", "one"),
			llm.ToolResult("b", "two"),
		},
	})

	want := []string{llm.RoleUser, llm.RoleAssistant, llm.RoleUser}
	got := roleList(t, body)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v — turns must alternate", got, want)
	}

	last := blocksOf(t, body, 2)
	if len(last) != 2 {
		t.Fatalf("final turn has %d blocks, want both tool results merged into it", len(last))
	}
	for i, id := range []string{"a", "b"} {
		b, _ := last[i].(map[string]any)
		if b["type"] != "tool_result" || b["tool_use_id"] != id {
			t.Errorf("block %d = %#v, want a tool_result for %q", i, b, id)
		}
	}

	// The assistant's calls travel as tool_use blocks, not a separate field.
	asst := blocksOf(t, body, 1)
	if len(asst) != 2 {
		t.Fatalf("assistant turn has %d blocks, want its two tool_use blocks", len(asst))
	}
	if b, _ := asst[0].(map[string]any); b["type"] != "tool_use" || b["name"] != "read" {
		t.Errorf("assistant block = %#v, want a tool_use naming the tool", b)
	}
}

// max_tokens is required here, unlike chat-completions where it may be
// omitted. A request without it is a 400, so an unset max_output must not
// become a missing field.
func TestAnthropicAlwaysSendsMaxTokens(t *testing.T) {
	if got := antBody(t, llm.Request{Model: "m"})["max_tokens"]; got != float64(defaultMaxTokens) {
		t.Errorf("max_tokens = %v with no max_output, want the default %d", got, defaultMaxTokens)
	}
	if got := antBody(t, llm.Request{Model: "m", MaxTokens: 4096})["max_tokens"]; got != float64(4096) {
		t.Errorf("max_tokens = %v, want the model's own 4096", got)
	}
}

// Tools carry their schema under input_schema, not function.parameters.
func TestAnthropicToolSchema(t *testing.T) {
	body := antBody(t, llm.Request{
		Model:      "m",
		ToolChoice: "auto",
		Tools: []llm.ToolDef{{
			Name:        "read",
			Description: "Read a file.",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one", body["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["input_schema"] == nil {
		t.Errorf("tool = %#v, want the schema under input_schema", tool)
	}
	if _, isOpenAIShape := tool["function"]; isOpenAIShape {
		t.Errorf("tool = %#v, must not carry the chat-completions shape", tool)
	}
	if tc, _ := body["tool_choice"].(map[string]any); tc == nil || tc["type"] != "auto" {
		t.Errorf("tool_choice = %#v, want {\"type\":\"auto\"}", body["tool_choice"])
	}
}

// antStream drives the parser over a canned stream and collects the events.
func antStream(t *testing.T, sse string) []llm.StreamEvent {
	t.Helper()
	c := NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic, APIKey: "k"})
	var out []llm.StreamEvent
	ok := c.parseStream(strings.NewReader(sse), func(ev llm.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		out = append(out, ev)
		return true
	})
	if !ok {
		t.Fatal("parseStream stopped early")
	}
	return out
}

// The two fixtures below are real captures from OpenRouter's Anthropic
// endpoint, kept verbatim — gateway extensions, the non-standard [DONE]
// sentinel and all. They are the reason the parser is shaped the way it is,
// and a specification-only reading of the protocol produces a parser that
// passes the first and mangles the second.
func replayFixture(t *testing.T, name string) []llm.StreamEvent {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return antStream(t, string(b))
}

func toolCallsFrom(evs []llm.StreamEvent) map[int]*llm.ToolCall {
	calls := map[int]*llm.ToolCall{}
	for _, ev := range evs {
		if ev.Kind != llm.EventToolCall || ev.ToolCall == nil {
			continue
		}
		d := ev.ToolCall
		c, ok := calls[d.Index]
		if !ok {
			c = &llm.ToolCall{}
			calls[d.Index] = c
		}
		if d.ID != "" {
			c.ID = d.ID
		}
		if d.Name != "" {
			c.Name = d.Name
		}
		c.Arguments += d.Args
	}
	return calls
}

func textOf(evs []llm.StreamEvent, kind llm.EventKind) string {
	var b strings.Builder
	for _, ev := range evs {
		if ev.Kind == kind {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// claude-haiku-4.5 answers with a single tool_use block at index 0.
func TestAnthropicStreamSingleBlock(t *testing.T) {
	evs := replayFixture(t, "anthropic-stream-haiku.sse")

	calls := toolCallsFrom(evs)
	if len(calls) != 1 {
		t.Fatalf("assembled %d tool calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Name != "read" || !strings.HasPrefix(c.ID, "toolu_") {
		t.Errorf("call = %+v, want the read tool with its id", c)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
		t.Fatalf("arguments %q did not reassemble into JSON: %v", c.Arguments, err)
	}
	if args["path"] != "main.go" || args["lines"] != float64(40) {
		t.Errorf("arguments = %v, want path main.go and lines 40", args)
	}

	if got := finishOf(evs); got != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls — the coder branches on the chat-completions names", got)
	}
	u := usageOf(evs)
	if u == nil || u.PromptTokens != 587 || u.CompletionTokens != 71 {
		t.Errorf("usage = %+v, want 587 prompt and 71 completion", u)
	}
}

// xiaomi/mimo-v2.5, through the same endpoint, sends a thinking block at index
// 0 and the tool_use at index 1. This is the case a single-block parser gets
// wrong: it would attribute the thinking text to the tool call, or the tool
// arguments to the wrong index, and it would do so only for some models.
func TestAnthropicStreamMultipleBlocks(t *testing.T) {
	evs := replayFixture(t, "anthropic-stream-mimo.sse")

	if got := textOf(evs, llm.EventReasoning); !strings.Contains(got, "read the first") {
		t.Errorf("reasoning = %q, want the thinking block's text", got)
	}
	// The thinking text must not have leaked into the tool call.
	calls := toolCallsFrom(evs)
	if len(calls) != 1 {
		t.Fatalf("assembled %d tool calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Name != "read" {
		t.Errorf("call = %+v, want the read tool", c)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
		t.Fatalf("arguments %q did not reassemble into JSON: %v — a block index was mixed up", c.Arguments, err)
	}
	if args["path"] != "main.go" {
		t.Errorf("arguments = %v, want path main.go", args)
	}

	// message_start reported input_tokens 0 for this model; the real count
	// arrives only on message_delta. Taking the first would price it at zero.
	u := usageOf(evs)
	if u == nil || u.PromptTokens == 0 {
		t.Errorf("usage = %+v, want the prompt tokens from message_delta, not the zero from message_start", u)
	}
}

func finishOf(evs []llm.StreamEvent) string {
	for _, ev := range evs {
		if ev.Kind == llm.EventFinish {
			return ev.FinishReason
		}
	}
	return ""
}

func usageOf(evs []llm.StreamEvent) *llm.Usage {
	var last *llm.Usage
	for _, ev := range evs {
		if ev.Kind == llm.EventUsage {
			last = ev.Usage
		}
	}
	return last
}

// Three tool_use blocks in one turn — the case that separates a parser which
// attributes fragments by block index from one that accumulates globally.
// With a single accumulator all three sets of arguments concatenate into one
// unparseable string, and the harness runs one garbled call instead of three.
//
// This fixture exists because the two above did *not* catch that: a response
// with one tool call has index 0 either way, so both parsers passed it. The
// test that proved nothing is worse than no test, and only a captured parallel
// call shows the difference.
func TestAnthropicStreamParallelToolCalls(t *testing.T) {
	evs := replayFixture(t, "anthropic-stream-parallel.sse")

	calls := toolCallsFrom(evs)
	if len(calls) != 3 {
		t.Fatalf("assembled %d tool calls, want 3 — fragments were attributed to the wrong block", len(calls))
	}
	seen := map[string]bool{}
	for i := range 3 {
		c, ok := calls[i]
		if !ok {
			t.Fatalf("no call at index %d; got %v", i, calls)
		}
		if c.Name != "read" || c.ID == "" {
			t.Errorf("call %d = %+v, want a named read with an id", i, c)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
			t.Fatalf("call %d arguments %q did not reassemble into JSON: %v", i, c.Arguments, err)
		}
		path, _ := args["path"].(string)
		if path == "" || seen[path] {
			t.Errorf("call %d path = %q, want a distinct file", i, path)
		}
		seen[path] = true
	}
	if got := finishOf(evs); got != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", got)
	}
}

// antSendOnce drives one request through a stubbed transport and returns it.
func antSendOnce(t *testing.T, p config.Provider) *http.Request {
	t.Helper()
	var got *http.Request
	c := NewAnthropic(p)
	c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return respond(200, "text/event-stream", "data: [DONE]\n"), nil
	})
	for _, err := range c.Send(context.Background(), llm.Request{
		Model: "m", Messages: []llm.Message{llm.TextMessage(llm.RoleUser, "hi")},
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func TestAnthropicEndpointsAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		adapter, wantURL string
		wantSession      bool
	}{
		{config.AdapterAnthropic, "https://api.anthropic.com/v1/messages", false},
		{config.AdapterOpenCodeAnthropic, "https://opencode.ai/zen/go/v1/messages", true},
	} {
		req := antSendOnce(t, config.Provider{Adapter: tc.adapter, APIKey: "k"})
		if got := req.URL.String(); got != tc.wantURL {
			t.Errorf("%s: posted to %s, want %s", tc.adapter, got, tc.wantURL)
		}
		// Anthropic rejects a request without the version header, and gateways
		// accept it, so it goes on every request rather than per destination.
		if got := req.Header.Get("Anthropic-Version"); got != anthropicVersion {
			t.Errorf("%s: anthropic-version = %q, want %q", tc.adapter, got, anthropicVersion)
		}
		// Anthropic's own API reads x-api-key; a gateway reads Authorization.
		if got := req.Header.Get("X-Api-Key"); got != "k" {
			t.Errorf("%s: x-api-key = %q, want the key", tc.adapter, got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("%s: Authorization = %q, want the bearer form too", tc.adapter, got)
		}
		if got := req.Header.Get("X-Opencode-Session"); (got != "") != tc.wantSession {
			t.Errorf("%s: x-opencode-session = %q, wantSent=%v", tc.adapter, got, tc.wantSession)
		}
	}

	// A base_url override still wins, which is what makes one adapter reach
	// OpenRouter's Anthropic endpoint as well.
	p := config.Provider{Adapter: config.AdapterAnthropic, BaseURL: "https://openrouter.ai/api/v1", APIKey: "k"}
	if got, want := antSendOnce(t, p).URL.String(), "https://openrouter.ai/api/v1/messages"; got != want {
		t.Errorf("base_url override: posted to %s, want %s", got, want)
	}
}

// ForProvider is the only place an adapter name becomes a wire protocol, so a
// name landing on the wrong dialect is the failure it has to rule out.
func TestForProviderPicksTheDialect(t *testing.T) {
	for _, tc := range []struct {
		adapter   string
		anthropic bool
	}{
		{config.AdapterOpenAI, false},
		{config.AdapterOpenRouter, false},
		{config.AdapterOpenCode, false},
		{config.AdapterAnthropic, true},
		{config.AdapterOpenCodeAnthropic, true},
	} {
		_, isAnthropic := ForProvider(config.Provider{Adapter: tc.adapter}).(*AnthropicClient)
		if isAnthropic != tc.anthropic {
			t.Errorf("%s: anthropic dialect = %v, want %v", tc.adapter, isAnthropic, tc.anthropic)
		}
	}
}

// A parameterless tool is the live pass's find. Strument's interrupt tool
// takes no arguments, so its Parameters map is nil and input_schema
// serializes as null — which chat-completions tolerates and Anthropic does
// not. It rejects the entire request, not just that tool, so one such tool in
// the set 400s every send. Nothing in the specification says so plainly and no
// unit test written from it would have caught it.
func TestAnthropicParameterlessToolGetsAnEmptySchema(t *testing.T) {
	body := antBody(t, llm.Request{
		Model: "m",
		Tools: []llm.ToolDef{
			{Name: "interrupt", Description: "End the turn."},
			{Name: "read", Parameters: map[string]any{"type": "object"}},
		},
	})
	tools, _ := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %#v, want 2", body["tools"])
	}
	first, _ := tools[0].(map[string]any)
	schema, ok := first["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema = %#v, want an object — null is rejected outright", first["input_schema"])
	}
	if schema["type"] != "object" {
		t.Errorf("input_schema = %#v, want an empty object schema", schema)
	}
}

// Providers disagree about the type of the in-band cost: OpenRouter sends
// 0.000942, opencode Go sends "0". A *float64 rejects the quoted form, and in
// the SSE parser a failed unmarshal ends the turn — so a decorative field
// would take the answer with it, after that answer was already on screen.
func TestCostAcceptsEitherType(t *testing.T) {
	for _, tc := range []struct {
		json      string
		wantKnown bool
		want      float64
	}{
		{`0.000942`, true, 0.000942},
		{`"0"`, true, 0},
		{`"0.25"`, true, 0.25},
		{`null`, false, 0},
		{`"free"`, false, 0}, // unparseable is "unknown", not an error
		{`{"nested":1}`, false, 0},
	} {
		var got flexFloat
		if err := json.Unmarshal([]byte(tc.json), &got); err != nil {
			t.Errorf("cost %s: err = %v, want it absorbed rather than raised", tc.json, err)
			continue
		}
		if got.Known != tc.wantKnown || (tc.wantKnown && got.Value != tc.want) {
			t.Errorf("cost %s = %+v, want known=%v value=%v", tc.json, got, tc.wantKnown, tc.want)
		}
		if p := got.ptr(); (p == nil) == tc.wantKnown {
			t.Errorf("cost %s: ptr() nil = %v, want %v", tc.json, p == nil, !tc.wantKnown)
		}
	}
}

// And the whole way through the stream parser: a quoted cost in a usage chunk
// must not end the turn.
func TestQuotedCostDoesNotKillTheStream(t *testing.T) {
	sse := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}," +
		"\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"cost\":\"0\"}}\n" +
		"data: [DONE]\n"

	evs := antStream(t, sse)
	if got := textOf(evs, llm.EventAnswer); got != "ok" {
		t.Errorf("answer = %q, want it delivered despite the quoted cost", got)
	}
	u := usageOf(evs)
	if u == nil || u.CompletionTokens != 2 {
		t.Fatalf("usage = %+v, want it reported", u)
	}
	if u.Cost == nil || *u.Cost != 0 {
		t.Errorf("cost = %v, want the quoted zero read as 0", u.Cost)
	}
}

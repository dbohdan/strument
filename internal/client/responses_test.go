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

func respBody(t *testing.T, req llm.Request) map[string]any {
	t.Helper()
	c := NewResponses(config.Provider{Adapter: config.AdapterResponses, APIKey: "k"})
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

// itemsOf returns the input list as maps.
func itemsOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, _ := body["input"].([]any)
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i], _ = r.(map[string]any)
	}
	return out
}

// There are no messages here, only items — so a tool call and its result stop
// being fields on a turn and become siblings in one flat list, paired by
// call_id. Getting that wrong does not fail loudly: the model simply loses
// track of what it asked for.
func TestResponsesFlattensToolCallsIntoItems(t *testing.T) {
	body := respBody(t, llm.Request{
		Model: "m",
		Messages: []llm.Message{
			llm.TextMessage(llm.RoleSystem, "be terse"),
			llm.TextMessage(llm.RoleUser, "read two files"),
			{Role: llm.RoleAssistant, Content: llm.TextContent("on it"), ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
				{ID: "c2", Name: "read", Arguments: `{"path":"b"}`},
			}},
			llm.ToolResult("c1", "one"),
			llm.ToolResult("c2", "two"),
		},
	})

	// The system prompt is not a role in this dialect.
	if got, _ := body["instructions"].(string); got != "be terse" {
		t.Errorf("instructions = %q, want the system text lifted out", got)
	}

	items := itemsOf(t, body)
	type shape struct{ typ, role, callID string }
	got := make([]shape, len(items))
	for i, it := range items {
		typ, _ := it["type"].(string)
		role, _ := it["role"].(string)
		id, _ := it["call_id"].(string)
		got[i] = shape{typ, role, id}
	}
	want := []shape{
		{"", llm.RoleUser, ""},
		{"", llm.RoleAssistant, ""},
		{"function_call", "", "c1"},
		{"function_call", "", "c2"},
		{"function_call_output", "", "c1"},
		{"function_call_output", "", "c2"},
	}
	if len(got) != len(want) {
		t.Fatalf("input has %d items, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Server-side storage would be a second copy of the conversation with its own
// lifetime, invisible to the user and outliving the request. Strument resends
// the history it owns, so this must stay off.
func TestResponsesDoesNotStoreServerSide(t *testing.T) {
	if got := respBody(t, llm.Request{Model: "m"})["store"]; got != false {
		t.Errorf("store = %v, want false — the conversation is the user's, not the endpoint's", got)
	}
}

// The tool shape is flat here: name and parameters sit on the tool, not under
// a nested "function" object as in chat-completions. And the parameterless
// tool trap is the same in all three dialects.
func TestResponsesToolShape(t *testing.T) {
	body := respBody(t, llm.Request{
		Model:      "m",
		ToolChoice: "auto",
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
	if first["name"] != "interrupt" {
		t.Errorf("tool = %#v, want name at the top level, not under \"function\"", first)
	}
	if _, nested := first["function"]; nested {
		t.Errorf("tool = %#v, must not carry the chat-completions nesting", first)
	}
	if _, ok := first["parameters"].(map[string]any); !ok {
		t.Errorf("parameters = %#v, want an object — null is rejected by strict schema checks",
			first["parameters"])
	}
}

// Reasoning is only ever visible as a summary here — the raw item carries an
// opaque encrypted_content blob — so an effort asks for one. But a reasoning
// object carrying *only* a summary is read by some providers as disabling
// reasoning: x-ai/grok-4.6 rejects it with "Reasoning is mandatory for this
// endpoint and cannot be disabled", where openai/gpt-5.6-luna accepts the same
// body. So "" and "default" must send no reasoning key at all.
func TestResponsesReasoningBlock(t *testing.T) {
	for _, tc := range []struct {
		effort      string
		wantBlock   bool
		wantEffort  string
		wantSummary bool
	}{
		{"", false, "", false},
		{"default", false, "", false},
		{"high", true, "high", true},
		{"off", true, "low", false},
	} {
		body := respBody(t, llm.Request{Model: "m", ReasoningEffort: tc.effort})
		r, hasBlock := body["reasoning"].(map[string]any)
		if hasBlock != tc.wantBlock {
			t.Errorf("effort %q: reasoning block present = %v, want %v (%#v)",
				tc.effort, hasBlock, tc.wantBlock, body["reasoning"])
			continue
		}
		if !tc.wantBlock {
			continue
		}
		if got, _ := r["effort"].(string); got != tc.wantEffort {
			t.Errorf("effort %q: sent effort %q, want %q", tc.effort, got, tc.wantEffort)
		}
		if _, has := r["summary"]; has != tc.wantSummary {
			t.Errorf("effort %q: summary requested = %v, want %v", tc.effort, has, tc.wantSummary)
		}
		// The shape grok rejects: a block with a summary and no effort.
		if _, hasEffort := r["effort"]; !hasEffort {
			t.Errorf("effort %q: reasoning block %#v has no effort — providers read that as disabling reasoning",
				tc.effort, r)
		}
	}
}

func respStreamOf(t *testing.T, name string) []llm.StreamEvent {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	c := NewResponses(config.Provider{Adapter: config.AdapterResponses, APIKey: "k"})
	var out []llm.StreamEvent
	if !c.parseStream(strings.NewReader(string(b)), func(ev llm.StreamEvent, err error) bool {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		out = append(out, ev)
		return true
	}) {
		t.Fatal("parseStream stopped early")
	}
	return out
}

// A real capture: gpt-5.6-luna reasons at output_index 0, then calls read
// twice at indices 1 and 2. output_index is the *item* index, so tool calls
// must be numbered separately — otherwise the coder is handed a call at index
// 1 with nothing at index 0, and the arguments land on the wrong call.
func TestResponsesStreamParallelToolCalls(t *testing.T) {
	evs := respStreamOf(t, "responses-stream-parallel.sse")

	calls := toolCallsFrom(evs)
	if len(calls) != 2 {
		t.Fatalf("assembled %d tool calls, want 2: %+v", len(calls), calls)
	}
	seen := map[string]bool{}
	for i := range 2 {
		c, ok := calls[i]
		if !ok {
			t.Fatalf("no call at index %d — tool calls are not numbered from zero: %+v", i, calls)
		}
		if c.Name != "read" || c.ID == "" {
			t.Errorf("call %d = %+v, want a named read with a call_id", i, c)
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
			t.Fatalf("call %d arguments %q did not reassemble: %v", i, c.Arguments, err)
		}
		path, _ := args["path"].(string)
		if path == "" || seen[path] {
			t.Errorf("call %d path = %q, want a distinct file", i, path)
		}
		seen[path] = true
	}
	if u := usageOf(evs); u == nil || u.PromptTokens == 0 {
		t.Errorf("usage = %+v, want it read from response.completed", u)
	}
}

// And the other item kinds: a reasoning summary streams as its own delta type,
// separate from the answer text, so the two must not be merged.
func TestResponsesStreamReasoningAndText(t *testing.T) {
	evs := respStreamOf(t, "responses-stream-reasoning.sse")

	reasoning := textOf(evs, llm.EventReasoning)
	answer := textOf(evs, llm.EventAnswer)
	if reasoning == "" {
		t.Error("no reasoning text — the summary deltas were not recognized")
	}
	if answer == "" {
		t.Error("no answer text")
	}
	if strings.Contains(answer, reasoning) && reasoning != "" {
		t.Error("the reasoning summary leaked into the answer")
	}
	if !strings.Contains(answer, "391") {
		t.Errorf("answer = %q, want the arithmetic result", answer)
	}
	if got := finishOf(evs); got != "stop" {
		t.Errorf("finish reason = %q, want stop", got)
	}
}

func TestResponsesEndpointsAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		adapter, wantURL string
		wantSession      bool
	}{
		{config.AdapterResponses, "https://api.openai.com/v1/responses", false},
		{config.AdapterOpenCodeResponses, "https://opencode.ai/zen/go/v1/responses", true},
	} {
		var got *http.Request
		c := NewResponses(config.Provider{Adapter: tc.adapter, APIKey: "k"})
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
		if u := got.URL.String(); u != tc.wantURL {
			t.Errorf("%s: posted to %s, want %s", tc.adapter, u, tc.wantURL)
		}
		if h := got.Header.Get("X-Opencode-Session"); (h != "") != tc.wantSession {
			t.Errorf("%s: x-opencode-session = %q, wantSent=%v", tc.adapter, h, tc.wantSession)
		}
		if h := got.Header.Get("Authorization"); h != "Bearer k" {
			t.Errorf("%s: Authorization = %q", tc.adapter, h)
		}
	}
}

func TestForProviderPicksTheResponsesDialect(t *testing.T) {
	for _, adapter := range []string{config.AdapterResponses, config.AdapterOpenCodeResponses} {
		if _, ok := ForProvider(config.Provider{Adapter: adapter}).(*ResponsesClient); !ok {
			t.Errorf("%s: did not route to the Responses client", adapter)
		}
	}
	for _, adapter := range []string{
		config.AdapterOpenAI, config.AdapterOpenRouter, config.AdapterOpenCode,
		config.AdapterAnthropic, config.AdapterOpenCodeAnthropic,
	} {
		if _, wrong := ForProvider(config.Provider{Adapter: adapter}).(*ResponsesClient); wrong {
			t.Errorf("%s: routed to the Responses client", adapter)
		}
	}
}

package client

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// The events are opencode Go's own, captured 2026-11-19 from a streaming
// request in each dialect: usage.opencode rides beside a cost of 0.
const ocBillingJSON = `"cost":0,"opencode":{"billing_source":"go","list_cost":1.587e-05,"allowance":4.761e-05,` +
	`"tokens":{"input":81,"output":3,"reasoning":0,"cache_read":128,"cache_write_5m":0,"cache_write_1h":0}}`

func wantGoBilling(t *testing.T, dialect string, u *llm.Usage) {
	t.Helper()
	if u == nil || u.Opencode == nil {
		t.Fatalf("%s: usage %+v has no opencode billing", dialect, u)
	}
	if b := *u.Opencode; b.Source != "go" || b.Allowance != 4.761e-05 || b.ListCost != 1.587e-05 {
		t.Errorf("%s: billing = %+v", dialect, b)
	}
	if u.Cost == nil || *u.Cost != 0 {
		t.Errorf("%s: cost = %v, want the reported 0", dialect, u.Cost)
	}
}

func TestOpencodeBillingInEachDialect(t *testing.T) {
	chat := `data: {"choices":[{"index":0,"delta":{"content":"OK"}}]}` + "\n" +
		`data: {"choices":[{"index":0,"finish_reason":"stop","delta":{"content":""}}],` +
		`"usage":{"prompt_tokens":209,"completion_tokens":3,` + ocBillingJSON + `}}` + "\n" +
		"data: [DONE]\n"
	var evs []llm.StreamEvent
	for ev, err := range ParseSSE(strings.NewReader(chat)) {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	wantGoBilling(t, "chat completions", usageOf(evs))

	messages := `data: {"type":"message_start","message":{"usage":{"input_tokens":0,"output_tokens":0}}}` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},` +
		`"usage":{"input_tokens":81,"output_tokens":3,"cache_read_input_tokens":128,` + ocBillingJSON + `}}` + "\n"
	wantGoBilling(t, "messages", usageOf(antStream(t, messages)))

	responses := `data: {"type":"response.output_text.delta","delta":"OK"}` + "\n" +
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":209,` +
		`"input_tokens_details":{"cached_tokens":128},"output_tokens":3,` + ocBillingJSON + `}}}` + "\n"
	c := NewResponses(config.Provider{Adapter: config.AdapterOpenCodeResponses, APIKey: "k"})
	evs = nil
	if !c.parseStream(strings.NewReader(responses), func(ev llm.StreamEvent, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
		return true
	}) {
		t.Fatal("parseStream stopped early")
	}
	wantGoBilling(t, "responses", usageOf(evs))
}

// Elsewhere there is no opencode object, and no billing is made up.
func TestNoOpencodeBillingElsewhere(t *testing.T) {
	chat := `data: {"choices":[{"index":0,"finish_reason":"stop","delta":{}}],` +
		`"usage":{"prompt_tokens":5,"completion_tokens":1,"cost":0.000942}}` + "\n" + "data: [DONE]\n"
	var evs []llm.StreamEvent
	for ev, err := range ParseSSE(strings.NewReader(chat)) {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	if u := usageOf(evs); u == nil || u.Opencode != nil {
		t.Errorf("usage = %+v, want no opencode billing", u)
	}
}

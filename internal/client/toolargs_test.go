package client

import (
	"encoding/json"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// truncatedCall is a write call cut off mid-argument, which is what a reply
// that hits the output limit inside a tool call leaves behind. Shortened from
// the 6.5 KB one that produced the bug; the shape is what matters.
const truncatedArgs = `{"path": "/tmp/x.html", "content": "<!DOCTYPE htm`

func toolCallRequest(args string) llm.Request {
	return llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: llm.TextContent("write a file")},
			{
				Role:      llm.RoleAssistant,
				Content:   llm.TextContent("I'll write it."),
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "write", Arguments: args}},
			},
			llm.ToolResult("call_1", "The arguments were not valid JSON: unexpected end of JSON input"),
		},
	}
}

// No dialect may put a malformed argument string on the wire.
//
// This is not a style rule. A provider that validates the field rejects the
// whole request, and the malformed call is in the conversation now, so it goes
// out again with every later turn: one truncated call ends the session. Seen
// against OpenRouter, which answered "Assistant tool call function.arguments
// must be valid JSON" nine times in a row, once per retry, and would have
// answered it forever.
//
// Asserted through BuildBody rather than by grepping the clients for a call to
// WireArguments, because the question is what the bytes say. Anthropic and the
// Responses client each solved the narrower empty-string case locally, which is
// precisely how chat-completions came to be the one still sending it raw: three
// copies of a rule is three chances to have two of them.
func TestNoDialectSendsMalformedToolArguments(t *testing.T) {
	for _, args := range []struct{ name, value string }{
		{"truncated", truncatedArgs},
		{"empty", ""},
		{"whitespace", "  "},
	} {
		t.Run(args.name, func(t *testing.T) {
			req := toolCallRequest(args.value)

			t.Run("chat completions", func(t *testing.T) {
				body := New(config.Provider{Adapter: config.AdapterOpenRouter}).BuildBody(req)
				var msgs []struct {
					ToolCalls []struct {
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				}
				decode(t, body["messages"], &msgs)
				assertValidArgs(t, findArgs(msgs))
			})

			t.Run("anthropic", func(t *testing.T) {
				body := NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic}).BuildBody(req)
				// Anthropic's input is an object, not a string holding one, so
				// an invalid value cannot even be marshalled as raw JSON here —
				// it corrupts the whole body. Checking the body parses at all
				// is the assertion.
				got := marshal(t, body["messages"])
				if !json.Valid([]byte(got)) {
					t.Fatalf("the request body is not valid JSON:\n%s", got)
				}
				if strings.Contains(got, "DOCTYPE htm\"") {
					t.Errorf("the truncated fragment reached the wire:\n%s", got)
				}
			})

			t.Run("responses", func(t *testing.T) {
				body := NewResponses(config.Provider{Adapter: config.AdapterResponses}).BuildBody(req)
				var items []struct {
					Type      string `json:"type"`
					Arguments string `json:"arguments"`
				}
				decode(t, body["input"], &items)
				var got []string
				for _, it := range items {
					if it.Type == "function_call" {
						got = append(got, it.Arguments)
					}
				}
				assertValidArgs(t, got)
			})
		})
	}
}

// The counter-arm: a call whose arguments are fine must cross every dialect
// byte for byte. A repair that rewrote every call to "{}" would pass every
// assertion above and break the harness completely.
func TestValidToolArgumentsAreUntouched(t *testing.T) {
	const good = `{"path":"a.txt","content":"hello"}`
	req := toolCallRequest(good)

	for name, got := range map[string]string{
		"chat completions": marshal(t, New(config.Provider{Adapter: config.AdapterOpenRouter}).BuildBody(req)["messages"]),
		"anthropic":        marshal(t, NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic}).BuildBody(req)["messages"]),
		"responses":        marshal(t, NewResponses(config.Provider{Adapter: config.AdapterResponses}).BuildBody(req)["input"]),
	} {
		if !strings.Contains(got, "a.txt") || !strings.Contains(got, "hello") {
			t.Errorf("%s dropped a valid call's arguments:\n%s", name, got)
		}
	}
}

func findArgs(msgs []struct {
	ToolCalls []struct {
		Function struct {
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
},
) []string {
	var out []string
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			out = append(out, tc.Function.Arguments)
		}
	}
	return out
}

func assertValidArgs(t *testing.T, args []string) {
	t.Helper()
	if len(args) != 1 {
		t.Fatalf("found %d tool calls in the body, want 1; the assertion below would check nothing", len(args))
	}
	for _, a := range args {
		if !json.Valid([]byte(a)) {
			t.Errorf("arguments on the wire are not valid JSON: %q", a)
		}
	}
}

func decode(t *testing.T, v any, into any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}

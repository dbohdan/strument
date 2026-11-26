package client

import (
	"context"
	"iter"
	"net/http"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// Every dialect sends the config's headers, keeps the credential it sets
// itself, and lets a config header replace a default such as User-Agent.
func TestConfigHeadersReachTheWire(t *testing.T) {
	type sender interface {
		Send(ctx context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error]
	}
	var got http.Header
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		return respond(400, "application/json", `{"error":{"message":"stop here"}}`), nil
	})
	for name, mk := range map[string]func() sender{
		"chat": func() sender {
			c := New(config.Provider{Adapter: config.AdapterOpenAI, APIKey: "k"})
			c.Transport = rt
			return c
		},
		"anthropic": func() sender {
			c := NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic, APIKey: "k"})
			c.Transport = rt
			return c
		},
		"responses": func() sender {
			c := NewResponses(config.Provider{Adapter: config.AdapterResponses, APIKey: "k"})
			c.Transport = rt
			return c
		},
	} {
		got = nil
		req := llm.Request{Model: "m", Headers: map[string]string{"X-Cmd-Zdr": "1", "User-Agent": "custom/1"}}
		for range mk().Send(context.Background(), req) {
			break
		}
		if got == nil {
			t.Fatalf("%s: no request was sent", name)
		}
		if got.Get("X-Cmd-Zdr") != "1" {
			t.Errorf("%s: X-Cmd-Zdr = %q, want 1", name, got.Get("X-Cmd-Zdr"))
		}
		if got.Get("User-Agent") != "custom/1" {
			t.Errorf("%s: User-Agent = %q, want the config's", name, got.Get("User-Agent"))
		}
		if got.Get("Authorization") == "" && got.Get("X-Api-Key") == "" {
			t.Errorf("%s: the credential header is gone", name)
		}
	}
}

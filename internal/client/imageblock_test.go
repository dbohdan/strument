package client

import (
	"encoding/json"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// probeImage is the smallest thing that is unmistakably an image payload. The
// bytes are not a real PNG and do not need to be: nothing in these three
// adapters decodes them, and a fixture that carried a real one would carry
// kilobytes of base64 into every diff.
var probeImage = llm.ImageSource{
	MediaType: "image/png",
	Data:      "iVBORw0KGgo=",
	Label:     "diagram.png",
	Width:     1920,
	Height:    1080,
}

func imageRequest() llm.Request {
	return llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: llm.BlocksContent(
				llm.TextBlock("what is this?"),
				llm.ImageBlock(probeImage),
			)},
		},
	}
}

// Each dialect spells an image differently, and the differences are exactly the
// kind that source reading predicts wrongly: the chat-completions image_url is
// an object with a url field, the Responses one is a bare string under the same
// name, and Anthropic takes neither. Asserting the literal JSON is the point —
// a structural assertion would pass on all three shapes.
func TestImageBlockReachesEachDialect(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		c := NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic})
		body := c.BuildBody(imageRequest())
		got := marshal(t, body["messages"])
		want := `[{"role":"user","content":[{"type":"text","text":"what is this?"},` +
			`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]`
		if got != want {
			t.Errorf("messages =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("chat completions", func(t *testing.T) {
		c := New(config.Provider{Adapter: config.AdapterOpenRouter})
		body := c.BuildBody(imageRequest())
		got := marshal(t, body["messages"])
		want := `[{"role":"user","content":[{"type":"text","text":"what is this?"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]`
		if got != want {
			t.Errorf("messages =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("responses", func(t *testing.T) {
		c := NewResponses(config.Provider{Adapter: config.AdapterResponses})
		body := c.BuildBody(imageRequest())
		got := marshal(t, body["input"])
		want := `[{"role":"user","content":[{"type":"input_text","text":"what is this?"},` +
			`{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]}]`
		if got != want {
			t.Errorf("input =\n%s\nwant\n%s", got, want)
		}
	})
}

// A message with no image must serialize exactly as it did before the image
// work, or every recorded fixture and every cache prefix moves underneath us.
func TestTextOnlyContentIsUnchanged(t *testing.T) {
	req := llm.Request{
		Model:    "m",
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser, "plain")},
	}
	c := New(config.Provider{Adapter: config.AdapterOpenRouter})
	if got, want := marshal(t, c.BuildBody(req)["messages"]), `[{"role":"user","content":"plain"}]`; got != want {
		t.Errorf("chat completions = %s, want %s", got, want)
	}
	r := NewResponses(config.Provider{Adapter: config.AdapterResponses})
	if got, want := marshal(t, r.BuildBody(req)["input"]), `[{"role":"user","content":"plain"}]`; got != want {
		t.Errorf("responses = %s, want %s", got, want)
	}
}

// The backstop for a kind no client knows. It must arrive as visible text, not
// vanish: the whole hazard is that a dropped block reads to the user as the
// model being bad rather than as the harness losing the payload.
func TestUnknownBlockKindArrivesInBand(t *testing.T) {
	req := llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: llm.BlocksContent(llm.ContentBlock{Type: "hologram"})},
		},
	}
	for name, got := range map[string]string{
		"anthropic":        marshal(t, NewAnthropic(config.Provider{Adapter: config.AdapterAnthropic}).BuildBody(req)["messages"]),
		"chat completions": marshal(t, New(config.Provider{Adapter: config.AdapterOpenRouter}).BuildBody(req)["messages"]),
		"responses":        marshal(t, NewResponses(config.Provider{Adapter: config.AdapterResponses}).BuildBody(req)["input"]),
	} {
		if !strings.Contains(got, "unsupported content block hologram") {
			t.Errorf("%s dropped an unknown block silently: %s", name, got)
		}
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

package llm

import (
	"context"
	"iter"
)

// Request is one chat-completion request as the coder hands it to the client.
// The client's adapter translates it to the provider dialect.
type Request struct {
	Model           string
	Messages        []Message
	Temperature     *float64
	ReasoningEffort string         // request-side effort, e.g. "low"; "" => omit
	ExtraParams     map[string]any // fenced passthrough
	Tools           []ToolDef      // function tools offered to the model; nil => none
	ToolChoice      string         // "auto" | "none" | ""; "" => omit

	// MaxTokens caps the response. Zero means "unset": the OpenAI dialect
	// omits it and lets the provider decide, but Anthropic's Messages API
	// requires the field, so that client substitutes a default rather than
	// sending a request the endpoint will reject.
	MaxTokens int
}

// ToolDef is a function tool offered to the model. Parameters is a JSON
// Schema object.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ModelClient is the port the coder streams a model response through.
// Implementations: the HTTP client (internal/client)
// and the fixture replay stub (internal/fixture).
type ModelClient interface {
	Send(ctx context.Context, req Request) iter.Seq2[StreamEvent, error]
}

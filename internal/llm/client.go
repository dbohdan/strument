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
}

// ModelClient is the port the coder streams a model response through.
// Implementations: the HTTP client (internal/client)
// and the fixture replay stub (internal/fixture).
type ModelClient interface {
	Send(ctx context.Context, req Request) iter.Seq2[StreamEvent, error]
}

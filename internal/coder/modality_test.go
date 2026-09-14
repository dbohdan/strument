package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

func imageMsg() llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: llm.BlocksContent(
		llm.TextBlock("what is this?"),
		llm.ImageBlock(llm.ImageSource{MediaType: "image/png", Label: "diagram.png", Width: 800, Height: 600}),
	)}
}

var seeing = &config.Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}

// The case the whole design turns on: /model switched to a text-only model
// while an image is already in the history. The conversation has to stay
// sendable, and the model has to be told rather than left to invent.
func TestImageProjectedForATextOnlyModel(t *testing.T) {
	got := projectForModel([]llm.Message{imageMsg()}, &config.Model{})
	if len(got[0].Content.Images()) != 0 {
		t.Fatal("an image survived projection to a text-only model")
	}
	text := got[0].Content.String()
	for _, want := range []string{"what is this?", "diagram.png", "does not accept image input", "do not guess"} {
		if !strings.Contains(text, want) {
			t.Errorf("projected text is missing %q:\n%s", want, text)
		}
	}
}

func TestImageKeptForAModelThatSees(t *testing.T) {
	in := []llm.Message{imageMsg()}
	got := projectForModel(in, seeing)
	if len(got[0].Content.Images()) != 1 {
		t.Error("a declared vision model lost its image")
	}
	// Same backing array: projecting nothing must not rewrite the request, or
	// the prompt-cache prefix moves on every turn for no reason.
	if &got[0] != &in[0] {
		t.Error("the message slice was copied when nothing needed projecting")
	}
}

// A nil model is "no model to send to". Guessing that it can see images is the
// only choice with a wire error at the end of it.
func TestNilModelIsTextOnly(t *testing.T) {
	got := projectForModel([]llm.Message{imageMsg()}, nil)
	if len(got[0].Content.Images()) != 0 {
		t.Error("an image went out under a nil model")
	}
}

// Cache breakpoints are placed by message position in assemble.go. If a
// projected block dropped the breakpoint that was on the image, every
// breakpoint after it would move and the turn would pay for a cache write.
func TestProjectionCarriesTheCacheBreakpoint(t *testing.T) {
	img := llm.ImageBlock(llm.ImageSource{MediaType: "image/png", Label: "x.png"})
	img.CacheControl = &llm.CacheControl{Type: "ephemeral", TTL: "1h"}
	in := []llm.Message{{Role: llm.RoleUser, Content: llm.BlocksContent(img)}}

	got := projectForModel(in, &config.Model{})
	cc := got[0].Content.Blocks[0].CacheControl
	if cc == nil || cc.TTL != "1h" {
		t.Errorf("the breakpoint did not survive projection: %+v", cc)
	}
}

// Projection must not touch the input. The send path calls this on the slice
// it is about to reuse, and a mutating "pure" function would corrupt the
// conversation rather than one request.
func TestProjectionDoesNotMutateItsInput(t *testing.T) {
	in := []llm.Message{imageMsg()}
	_ = projectForModel(in, &config.Model{})
	if len(in[0].Content.Images()) != 1 {
		t.Error("projectForModel mutated the caller's messages")
	}
}

// Plain text is the overwhelmingly common case and must cost nothing.
func TestTextOnlyConversationIsUntouched(t *testing.T) {
	in := []llm.Message{llm.TextMessage(llm.RoleUser, "hello")}
	if got := projectForModel(in, &config.Model{}); &got[0] != &in[0] {
		t.Error("a text-only conversation was rebuilt")
	}
}

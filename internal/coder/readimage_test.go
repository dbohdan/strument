package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

const readImageScenario = `
{"kind":"meta","v":1,"scenario":"read-image","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"look at diagram.png"}
{"kind":"stream","events":[{"kind":"ToolCall","tool_index":0,"tool_id":"c1","tool_name":"read","tool_args":"{\"path\":\"diagram.png\"}"},{"kind":"Finish","finish_reason":"tool_calls"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"A flowchart."},{"kind":"Finish","finish_reason":"stop"}]}
`

// The model's own route to an image: it calls read, and the picture comes back
// in the tool result rather than a refusal about the file not being UTF-8.
func TestReadToolReturnsAnImage(t *testing.T) {
	sc := inlineScenario(t, readImageScenario)
	env := setupScenario(t, sc, func(c *Coder) {
		c.editFormat = "tool"
		c.Model.InputModalities = []string{llm.BlockText, llm.BlockImage}
	})
	writeTestPNG(t, filepath.Join(env.coder.Root, "diagram.png"), 40, 30)

	var toolImages []llm.ImageSource
	env.stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool {
				if got := m.Content.Images(); len(got) > 0 {
					toolImages = got
				}
			}
		}
		return nil
	}
	env.coder.Run(t.Context(), sc.User)

	if len(toolImages) != 1 {
		t.Fatalf("the tool result carried %d images", len(toolImages))
	}
	if toolImages[0].MediaType != "image/png" || toolImages[0].Width != 40 {
		t.Errorf("the image came back wrong: %+v", toolImages[0])
	}
	// The model chose this path, so the label is the argument it used -- not a
	// base name, which is what /attach uses because the user typed that path.
	if toolImages[0].Label != "diagram.png" {
		t.Errorf("label = %q", toolImages[0].Label)
	}
}

// A text file still reads as text. The image branch must not shadow the path
// that every other read takes.
func TestReadToolStillReadsText(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	c.Out = testOutput{t}
	if err := os.WriteFile(filepath.Join(c.Root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, images := c.runRead(llm.ToolCall{Name: "read", Arguments: `{"path":"a.go"}`})
	if len(images) != 0 {
		t.Error("a text file came back as an image")
	}
	if !strings.Contains(text, "package a") {
		t.Errorf("the text was lost: %q", text)
	}
}

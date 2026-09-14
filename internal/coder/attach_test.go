package coder

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{B: 7, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const attachScenario = `
{"kind":"meta","v":1,"scenario":"attachment","source":"authored"}
{"kind":"chat","editable":[]}
{"kind":"user","text":"what is in this picture?"}
{"kind":"stream","events":[{"kind":"Answer","text":"A square."},{"kind":"Finish","finish_reason":"stop"}]}
`

// The end-to-end claim: a staged image is on the user turn of the request that
// goes out, and it is gone afterwards.
func TestAttachmentRidesOnTheNextRequestAndIsConsumed(t *testing.T) {
	sc := inlineScenario(t, attachScenario)
	env := setupScenario(t, sc, func(c *Coder) {
		c.Model.InputModalities = []string{llm.BlockText, llm.BlockImage}
	})
	writeTestPNG(t, filepath.Join(env.coder.Root, "shot.png"), 20, 10)
	if _, err := env.coder.AttachFile("shot.png"); err != nil {
		t.Fatal(err)
	}

	var images []llm.ImageSource
	var userText string
	env.stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		for _, m := range req.Messages {
			if m.Role != llm.RoleUser {
				continue
			}
			if got := m.Content.Images(); len(got) > 0 {
				images = got
				userText = m.Content.String()
			}
		}
		return nil
	}
	env.coder.Run(t.Context(), sc.User)

	if len(images) != 1 {
		t.Fatalf("the request carried %d images", len(images))
	}
	if images[0].MediaType != "image/png" || images[0].Data == "" {
		t.Errorf("the image arrived without usable bytes: %+v", images[0])
	}
	// The message the user typed has to still be in there beside it.
	if want := "what is in this picture?"; !strings.Contains(userText, want) {
		t.Errorf("the user's text was lost: %q", userText)
	}
	if len(env.coder.Attachments()) != 0 {
		t.Error("the attachment was not consumed; it would ride on the next message too")
	}
}

// A conversation that says nothing and only attaches must still be a valid
// message: no empty text block, which Anthropic rejects.
func TestAttachmentWithNoTextIsStillValid(t *testing.T) {
	c := New(t.TempDir(), &config.Model{
		Provider:        config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:            "m",
		InputModalities: []string{llm.BlockText, llm.BlockImage},
	})
	writeTestPNG(t, filepath.Join(c.Root, "shot.png"), 4, 4)
	if _, err := c.AttachFile("shot.png"); err != nil {
		t.Fatal(err)
	}

	msg, consumed := c.userMessage("")
	if len(consumed) != 1 {
		t.Fatalf("consumed %d", len(consumed))
	}
	for _, b := range msg.Content.Blocks {
		if b.Type == llm.BlockText && b.Text == "" {
			t.Error("an empty text block went into the message; providers reject those")
		}
	}
	if len(msg.Content.Images()) != 1 {
		t.Error("the image did not reach the message")
	}
}

// Refusals happen before anything is staged, so a failed attach cannot leave
// half a state behind.
func TestAttachRefusalsStageNothing(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	if err := os.WriteFile(filepath.Join(c.Root, "notes.txt"),
		bytes.Repeat([]byte("text\n"), 40), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.Root, "pics"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"notes.txt", "pics", "nope.png"} {
		if _, err := c.AttachFile(path); err == nil {
			t.Errorf("%s was accepted", path)
		}
	}
	if len(c.Attachments()) != 0 {
		t.Error("a refused attach left something staged")
	}
}

// The cap is there to catch a mistaken glob, so it must refuse the extra rather
// than silently keeping the first eight.
func TestAttachmentCountIsCapped(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	path := filepath.Join(c.Root, "shot.png")
	writeTestPNG(t, path, 4, 4)
	for i := range maxAttachments {
		if _, err := c.AttachFile("shot.png"); err != nil {
			t.Fatalf("attachment %d refused: %v", i, err)
		}
	}
	if _, err := c.AttachFile("shot.png"); err == nil {
		t.Error("the cap did not hold")
	}
	if len(c.Attachments()) != maxAttachments {
		t.Errorf("staged %d, want %d", len(c.Attachments()), maxAttachments)
	}
}

// Attachments returns a copy: a caller mutating the slice it gets must not be
// able to change what the next message will carry.
func TestAttachmentsAreNotAliased(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	writeTestPNG(t, filepath.Join(c.Root, "shot.png"), 4, 4)
	if _, err := c.AttachFile("shot.png"); err != nil {
		t.Fatal(err)
	}
	got := c.Attachments()
	got[0].Label = "clobbered"
	if c.Attachments()[0].Label == "clobbered" {
		t.Error("Attachments handed out the coder's own slice")
	}
}

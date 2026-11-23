package coder

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
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
	for i := range maxAttachments + 1 {
		writeTestPNG(t, filepath.Join(c.Root, fmt.Sprintf("shot%d.png", i)), i+1, 4)
	}
	for i := range maxAttachments {
		if _, err := c.AttachFile(fmt.Sprintf("shot%d.png", i)); err != nil {
			t.Fatalf("attachment %d refused: %v", i, err)
		}
	}
	if _, err := c.AttachFile(fmt.Sprintf("shot%d.png", maxAttachments)); err == nil {
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

// The label is a file name, never a path. Two platforms proved this the hard
// way in CI: on macOS t.TempDir() is under /var while the resolved root is
// under /private/var, and on Windows it is the 8.3 short name, so relativizing
// fell back to the absolute path. Beyond the test, an attachment usually comes
// from outside the project, and a label carrying the full path would put the
// user's home directory in front of the model on every screenshot.
func TestAttachmentLabelIsJustTheFileName(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	sub := filepath.Join(c.Root, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(sub, "shot.png"), 4, 4)

	// Through the project, and from an absolute path outside it.
	outside := filepath.Join(t.TempDir(), "elsewhere.png")
	writeTestPNG(t, outside, 5, 4)
	for _, path := range []string{filepath.Join("deep", "nested", "shot.png"), outside} {
		src, err := c.AttachFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if strings.ContainsAny(src.Label, `/\`) {
			t.Errorf("label %q carries a path", src.Label)
		}
	}
	if got := c.Attachments(); got[0].Label != "shot.png" || got[1].Label != "elsewhere.png" {
		t.Errorf("labels = %q, %q", got[0].Label, got[1].Label)
	}
}

// Attaching the same image twice stages it once: the second is refused, from
// the same path or another, so the model never sees one picture twice.
func TestAttachRefusesTheSameImageTwice(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	writeTestPNG(t, filepath.Join(c.Root, "shot.png"), 4, 4)
	writeTestPNG(t, filepath.Join(c.Root, "copy.png"), 4, 4)
	if _, err := c.AttachFile("shot.png"); err != nil {
		t.Fatal(err)
	}
	for _, again := range []string{"shot.png", "./shot.png", "copy.png"} {
		_, err := c.AttachFile(again)
		var dup AlreadyAttachedError
		if !errors.As(err, &dup) || dup.Label != "shot.png" {
			t.Errorf("attaching %s again: err = %v, want it refused as already attached as shot.png", again, err)
		}
	}
	if n := len(c.Attachments()); n != 1 {
		t.Errorf("%d staged, want 1", n)
	}
}

// The same file attached again after an edit is a second image, not a
// replacement: "compare these" is as likely a meaning as "use this instead".
// The versions are numbered in the order attached, and one can be dropped.
func TestAttachKeepsEachVersionOfAChangedFile(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	path := filepath.Join(c.Root, "shot.png")
	writeTestPNG(t, path, 4, 4)
	if _, err := c.AttachFile("shot.png"); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, path, 9, 9)
	src, err := c.AttachFile("shot.png")
	if err != nil || src.Label != "shot.png#2" || src.Width != 9 {
		t.Fatalf("the edited file: %+v, %v; want it staged as shot.png#2", src, err)
	}
	if got := labelsOf(c.Attachments()); !slices.Equal(got, []string{"shot.png#1", "shot.png#2"}) {
		t.Fatalf("labels %v", got)
	}
	if n := c.DropAttachments("shot.png#1"); n != 1 {
		t.Fatalf("dropping shot.png#1 removed %d", n)
	}
	if got := c.Attachments(); len(got) != 1 || got[0].Label != "shot.png" || got[0].Width != 9 {
		t.Errorf("after dropping the first version: %+v, want the edited one as shot.png", got)
	}
}

// Two different files with one name get labels long enough to tell apart, so
// the model can, and /attach drop can name one; dropping one shortens the
// other's label again. A bare name still drops every match, and says how many.
func TestAttachNamesTwinsApart(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	for i, dir := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(c.Root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestPNG(t, filepath.Join(c.Root, dir, "shot.png"), 4+i, 4)
		if _, err := c.AttachFile(filepath.Join(dir, "shot.png")); err != nil {
			t.Fatal(err)
		}
	}
	if got := labelsOf(c.Attachments()); !slices.Equal(got, []string{"a/shot.png", "b/shot.png"}) {
		t.Fatalf("labels %v, want a/shot.png and b/shot.png", got)
	}
	if n := c.DropAttachments("b/shot.png"); n != 1 {
		t.Fatalf("dropping b/shot.png removed %d", n)
	}
	if got := labelsOf(c.Attachments()); !slices.Equal(got, []string{"shot.png"}) {
		t.Errorf("after the twin left: %v, want shot.png", got)
	}
	writeTestPNG(t, filepath.Join(c.Root, "b", "shot.png"), 7, 4)
	if _, err := c.AttachFile(filepath.Join("b", "shot.png")); err != nil {
		t.Fatal(err)
	}
	if n := c.DropAttachments("shot.png"); n != 2 {
		t.Errorf("a bare name dropped %d, want both", n)
	}
}

func labelsOf(srcs []llm.ImageSource) []string {
	out := make([]string, len(srcs))
	for i, s := range srcs {
		out[i] = s.Label
	}
	return out
}

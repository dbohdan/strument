package repl

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 9, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAttachStagesAnImage(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Model = &config.Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}
	writePNG(t, filepath.Join(cdr.Root, "shot.png"), 32, 16)

	r.dispatch(context.Background(), "/attach shot.png")
	staged := cdr.Attachments()
	if len(staged) != 1 {
		t.Fatalf("staged %d images", len(staged))
	}
	if staged[0].MediaType != "image/png" || staged[0].Width != 32 || staged[0].Height != 16 {
		t.Errorf("staged %+v", staged[0])
	}
	// The echo has to name what was staged; an attachment nobody can see is a
	// surprise on the next request.
	if got := out.String(); !strings.Contains(got, "shot.png") || !strings.Contains(got, "32x16") {
		t.Errorf("the echo does not describe the attachment:\n%s", got)
	}
}

// Bare reports. The house pattern, and the reason /detach was rejected: a
// command typed to ask about state must not change it.
func TestBareAttachReportsAndChangesNothing(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Model = &config.Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}
	writePNG(t, filepath.Join(cdr.Root, "shot.png"), 4, 4)

	r.dispatch(context.Background(), "/attach")
	if len(cdr.Attachments()) != 0 {
		t.Error("bare /attach staged something")
	}
	r.dispatch(context.Background(), "/attach shot.png")
	r.dispatch(context.Background(), "/attach")
	if len(cdr.Attachments()) != 1 {
		t.Error("bare /attach changed what was staged")
	}
	if !strings.Contains(out.String(), "next message") {
		t.Errorf("the report does not say when they are sent:\n%s", out.String())
	}
}

func TestAttachDrop(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Model = &config.Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}
	for _, name := range []string{"a.png", "b.png"} {
		writePNG(t, filepath.Join(cdr.Root, name), 4, 4)
		r.dispatch(context.Background(), "/attach "+name)
	}

	r.dispatch(context.Background(), "/attach drop a.png")
	if got := cdr.Attachments(); len(got) != 1 || got[0].Label != "b.png" {
		t.Fatalf("after dropping a.png: %+v", got)
	}
	r.dispatch(context.Background(), "/attach drop")
	if len(cdr.Attachments()) != 0 {
		t.Error("bare drop left something attached")
	}
	// A name that matches nothing must say so rather than look like success.
	r.dispatch(context.Background(), "/attach drop nosuch.png")
	if !strings.Contains(out.String(), "nosuch.png") {
		t.Errorf("dropping an unknown name said nothing:\n%s", out.String())
	}
}

// The refusals have to say what is wrong. A provider error arriving three
// layers later says less and arrives after the user has paid for the request.
func TestAttachRefusesWhatItCannotSend(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Model = &config.Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}
	if err := os.WriteFile(filepath.Join(cdr.Root, "notes.txt"), []byte(strings.Repeat("package main\n", 40)), 0o644); err != nil {
		t.Fatal(err)
	}

	r.dispatch(context.Background(), "/attach notes.txt")
	r.dispatch(context.Background(), "/attach missing.png")
	if len(cdr.Attachments()) != 0 {
		t.Error("something unsendable was staged")
	}
	got := out.String()
	if !strings.Contains(got, "not a PNG") {
		t.Errorf("the refusal does not say why a text file cannot be attached:\n%s", got)
	}
	if !strings.Contains(got, "missing.png") {
		t.Errorf("the missing-file refusal does not name the file:\n%s", got)
	}
}

// Warn at the moment the user can still act on it, by switching models before
// they type their message.
func TestAttachWarnsWhenTheModelCannotSee(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Model = &config.Model{DisplayName: "text-only-1"}
	writePNG(t, filepath.Join(cdr.Root, "shot.png"), 4, 4)

	r.dispatch(context.Background(), "/attach shot.png")
	got := out.String()
	if !strings.Contains(got, "text-only-1") || !strings.Contains(got, "does not accept images") {
		t.Errorf("no warning for a text-only model:\n%s", got)
	}
	// Warned, not refused: the send path projects it to text, and refusing here
	// would not help the case that matters (an image already in the history
	// when /model switches).
	if len(cdr.Attachments()) != 1 {
		t.Error("the attachment was refused rather than flagged")
	}
}

// Staging is per-message, so it must not reach the resume file. dispatch saves
// when resumeState changes, and an attachment changing it would rewrite the
// file on every /attach for state that cannot be restored.
func TestAttachIsNotResumeState(t *testing.T) {
	r, _, root := replWithSaves(t)
	r.coder.Model = &config.Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}
	writePNG(t, filepath.Join(root, "shot.png"), 4, 4)

	before := r.resumeState()
	r.dispatch(context.Background(), "/attach shot.png")
	if r.resumeState() != before {
		t.Error("/attach changed resume state; attachments do not survive a restart and must not claim to")
	}
}

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

func turnBlock(n int, body string) string {
	return "## 2026-08-16 10:0" + string(rune('0'+n%10)) + ":00 — model\n\n" + body + "\n\n---\n\n"
}

// The transcript is trimmed from both ends, not just the recent one.
//
// A session has a shape: the opening turns carry the intent and the constraints
// the user stated, the recent turns carry the working state, and the middle is
// mechanics the code itself now records. A tail-only window dropped the reason
// for a decision — the one thing notes exist to preserve — while keeping the
// step-by-step of the last hour.
func TestSampleTranscriptKeepsBothEnds(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Strument chat history\n\n")
	b.WriteString(turnBlock(1, "OPENING-CONSTRAINT: 45 seconds because the load balancer idles at 60."))
	for i := range 400 {
		b.WriteString(turnBlock(2, strings.Repeat("middle mechanics ", 10)+string(rune('a'+i%26))))
	}
	b.WriteString(turnBlock(9, "CLOSING-STATE: the rename is half done."))
	full := b.String()

	if len(full) <= maxNotesInput {
		t.Fatalf("fixture is %d bytes, not big enough to force trimming", len(full))
	}
	got := sampleTranscript(full)

	if len(got) > maxNotesInput+200 {
		t.Errorf("sample is %d bytes, over the %d budget", len(got), maxNotesInput)
	}
	for _, want := range []string{"OPENING-CONSTRAINT", "CLOSING-STATE"} {
		if !strings.Contains(got, want) {
			t.Errorf("both ends must survive; %s is missing", want)
		}
	}
	if !strings.Contains(got, "omitted here") {
		t.Error("the gap should be named, so a model does not read the join as continuous")
	}
	// Cuts land on turn boundaries, so no half exchange is handed over.
	if i := strings.Index(got, "omitted here"); i > 0 {
		before := got[:i]
		if strings.Count(before, "## ") == 0 {
			t.Error("the head should contain whole turns")
		}
	}
}

// Short transcripts pass through untouched: no marker, no trimming.
func TestSampleTranscriptLeavesShortOnesAlone(t *testing.T) {
	in := "# Strument chat history\n\n" + turnBlock(1, "small")
	if got := sampleTranscript(in); got != in {
		t.Errorf("a short transcript was modified:\n%q", got)
	}
}

// The notes chunk's position is load-bearing. Breakpoints sit on
// examples-or-system and on read-only files, so anything between them rides
// inside the cached prefix — and a mid-session /read-only, which rewrites the
// read-only block, does not invalidate the notes with it.
func TestNotesChunkSitsInsideTheCachedPrefix(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.Model.Cache = true
	c.SessionNotes = "NOTES-MARKER: 45 because the load balancer idles at 60."
	c.SessionNotesDate = "2026-08-16 10:00"
	c.curMessages = []llm.Message{llm.TextMessage("user", "go on")}

	msgs := c.formatMessages().allMessages()

	var notesAt, roAt, curAt int
	for i, m := range msgs {
		switch {
		case strings.Contains(m.Text(), "NOTES-MARKER"):
			notesAt = i
		case strings.Contains(m.Text(), "read-only reference"):
			roAt = i
		case m.Text() == "go on":
			curAt = i
		}
	}
	if notesAt == 0 {
		t.Fatalf("the notes never reached the request:\n%+v", msgs)
	}
	if roAt != 0 && notesAt > roAt {
		t.Errorf("notes at %d must precede the read-only block at %d", notesAt, roAt)
	}
	if notesAt > curAt {
		t.Errorf("notes at %d must precede the conversation at %d", notesAt, curAt)
	}
	// The harness's own voice, not a fabricated turn from either party.
	if role := msgs[notesAt].Role; role != llm.RoleSystem {
		t.Errorf("notes are a %s message; they are the harness's artifact", role)
	}
	// Still at most two breakpoints: the notes must not buy a third.
	breakpoints := 0
	for _, m := range msgs {
		for _, b := range m.Content.Blocks {
			if b.CacheControl != nil {
				breakpoints++
			}
		}
	}
	if breakpoints > 2 {
		t.Errorf("breakpoints = %d, want at most 2", breakpoints)
	}
}

// No notes, no slot. A first-ever session must not carry a header announcing an
// absence.
func TestNoNotesNoSlot(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.curMessages = []llm.Message{llm.TextMessage("user", "go on")}
	for _, m := range c.formatMessages().allMessages() {
		if strings.Contains(m.Text(), "Notes from earlier work") {
			t.Errorf("an empty notes slot still emitted a header: %q", m.Text())
		}
	}
}

// The conflict rule is the counter-metric turned into an instruction: the
// failure this feature can cause is a model acting on a note the tree has moved
// past, so the note says which side loses.
func TestNotesHeaderNamesTheFilesAsAuthoritative(t *testing.T) {
	got := prompts.SessionNotesPrefix("2026-08-16 10:00")
	for _, want := range []string{"2026-08-16 10:00", "summary, not a record", "the files are right"} {
		if !strings.Contains(got, want) {
			t.Errorf("the notes header is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(prompts.SessionNotesPrefix(""), "date unknown") {
		t.Error("a missing date should say so rather than print an empty parenthetical")
	}
}

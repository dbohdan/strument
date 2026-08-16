package coder

import (
	"strings"
	"testing"
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

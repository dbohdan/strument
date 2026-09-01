package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// chunky is a client that streams the given text as events of the given size,
// token-delta style, then finishes.
type chunky struct {
	by   int
	text string
}

func (s chunky) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for start := 0; start < len(s.text); start += s.by {
			end := min(start+s.by, len(s.text))
			if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: s.text[start:end]}, nil) {
				return
			}
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// The report from the code-only trial: JSONL records holding tool results
// arrived one character per record ("T", "h", "e"...) while the rendered
// transcript was fine. No reproduction survived the investigation, and the
// suspicion fell on per-event emission somewhere in the record path. This test
// drives the whole record path with a maximally hostile stream — one-character
// answer deltas, the worst an SSE chunk can be — and pins that the answer is
// still exactly one message record.
//
// If this fails, the bug is in the record path and lives on the trunk. If it
// passes, the mangling did not happen where the report assumed, and the next
// suspect is the scorer's own parsing of the JSONL.
func TestRecordWithCharacterStream(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = chunky{by: 1, text: "The answer is 42."}

	c.runOne(context.Background(), "do the thing")

	var answers []Record
	for _, r := range rec.recs {
		if r.Type == "message" && r.Role == llm.RoleAssistant {
			answers = append(answers, r)
		}
	}
	if len(answers) != 1 {
		t.Fatalf("got %d assistant message records, want 1; the stream was cut per event", len(answers))
	}
	if answers[0].Text != "The answer is 42." {
		t.Errorf("assistant record text = %q", answers[0].Text)
	}
	// And no record anywhere carries a fragment of the answer. (Records with
	// no text — the turn header — are not fragments.)
	const answer = "The answer is 42."
	for _, r := range rec.recs {
		if r.Text == "" || len(r.Text) >= len(answer) || !strings.Contains(answer, r.Text) {
			continue
		}
		t.Errorf("a fragment landed as its own record: %+v", r)
	}
}

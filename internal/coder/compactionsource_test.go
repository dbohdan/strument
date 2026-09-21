// The arms of doc/experiments/2026-09-compaction-source.
//
// The pre-run checklist asks whether the arms differ in exactly the intended
// way, and answers "identical inputs mean the flag did nothing". These checks
// answer it deterministically, which is better than a pilot dump: they stay in
// the repo, and a change that quietly collapses the two arms fails the build
// rather than producing a clean null nobody can explain.

package coder

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// capturingStub records the text the summarizer was asked to condense.
type capturingStub struct{ inputs []string }

func (s *capturingStub) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	for _, m := range req.Messages {
		if m.Role == llm.RoleUser {
			s.inputs = append(s.inputs, m.Text())
		}
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "CONDENSED"}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func sourceSummary(t *testing.T) (*ChatSummary, *capturingStub) {
	t.Helper()
	stub := &capturingStub{}
	return NewChatSummary(stub, &config.Model{Slug: "w"}, RuneCounter{}, &summaryOutput{}, &fastClock{}, nil), stub
}

// The folded messages, which already hold the previous summary.
var foldedHistory = []llm.Message{
	llm.TextMessage(llm.RoleUser, "MESSAGE-ONLY-MARKER: what should the poll interval be?"),
	llm.TextMessage(llm.RoleAssistant, "Forty-five seconds."),
}

const recordText = "# Strument chat history\n\n## 2026-09-20 10:00:00 — m\n\n" +
	"### Prompt\n\nRECORD-ONLY-MARKER: what should the poll interval be?\n\n" +
	"### Response\n\nForty-five seconds.\n\n---\n\n"

// The two arms send different text, and each sends its own source.
func TestTheTwoCompactionSourcesDiffer(t *testing.T) {
	fold, foldStub := sourceSummary(t)
	if _, err := fold.summarizeHead(foldedHistory); err != nil {
		t.Fatal(err)
	}

	record, recordStub := sourceSummary(t)
	record.Record = func() string { return recordText }
	if _, err := record.summarizeHead(foldedHistory); err != nil {
		t.Fatal(err)
	}

	if len(foldStub.inputs) != 1 || len(recordStub.inputs) != 1 {
		t.Fatalf("inputs: fold=%d record=%d, want one each", len(foldStub.inputs), len(recordStub.inputs))
	}
	if foldStub.inputs[0] == recordStub.inputs[0] {
		t.Fatal("both arms sent the same text; the flag did nothing")
	}
	if !strings.Contains(foldStub.inputs[0], "MESSAGE-ONLY-MARKER") {
		t.Errorf("the fold arm did not summarize the messages:\n%s", foldStub.inputs[0])
	}
	if strings.Contains(recordStub.inputs[0], "MESSAGE-ONLY-MARKER") {
		t.Errorf("the record arm summarized the messages it was meant to replace:\n%s", recordStub.inputs[0])
	}
	if !strings.Contains(recordStub.inputs[0], "RECORD-ONLY-MARKER") {
		t.Errorf("the record arm did not summarize the record:\n%s", recordStub.inputs[0])
	}
}

// Both arms still produce a summary in the shape the rest of the code expects:
// a marked harness turn, which is what isSummaryMessage recognises and what
// keeps a later fold from erasing an earlier one.
func TestBothSourcesProduceAMarkedSummary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record func() string
	}{
		{"fold", nil},
		{"record", func() string { return recordText }},
	} {
		s, _ := sourceSummary(t)
		s.Record = tc.record
		out, err := s.summarizeHead(foldedHistory)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(out) != 1 {
			t.Fatalf("%s: %d messages, want one", tc.name, len(out))
		}
		if out[0].Role != llm.RoleUser {
			t.Errorf("%s: summary is a %s message, want a marked user turn", tc.name, out[0].Role)
		}
		if !isSummaryMessage(out[0]) {
			t.Errorf("%s: the summary is not recognisable as one: %q", tc.name, out[0].Text())
		}
	}
}

// A session with nothing recorded — --no-history, or a first fold before
// anything reached disk — folds the messages, because that is what there is.
func TestTheRecordSourceFallsBackWhenThereIsNoRecord(t *testing.T) {
	for _, empty := range []func() string{
		func() string { return "" },
		func() string { return "   \n\t " },
	} {
		s, stub := sourceSummary(t)
		s.Record = empty
		if _, err := s.summarizeHead(foldedHistory); err != nil {
			t.Fatal(err)
		}
		if len(stub.inputs) != 1 || !strings.Contains(stub.inputs[0], "MESSAGE-ONLY-MARKER") {
			t.Errorf("an empty record did not fall back to the messages: %q", stub.inputs)
		}
	}
}

// The record arm is bounded the way notes are. Without this its input grows
// with the session and eventually exceeds the side model's window — which is
// the thing compaction exists to prevent, reintroduced by the fix for it.
func TestTheRecordSourceIsBounded(t *testing.T) {
	// Shaped like a record, not merely large. sampleTranscript trims the head
	// back to the last turn heading so it ends on a whole turn, so a fixture
	// of headingless filler loses its only turn to that trim — which is what
	// the first draft of this test did, and it was the fixture that was wrong.
	var b strings.Builder
	b.WriteString(recordText)
	for i := range 400 {
		fmt.Fprintf(&b, "## 2026-09-20 10:%02d:00 — m\n\n### Prompt\n\nburying turn %d\n\n"+
			"### Response\n\n%s\n\n---\n\n", i%60, i, strings.Repeat("filler. ", 20))
	}
	huge := b.String()
	if len(huge) <= maxNotesInput {
		t.Fatalf("the fixture is %d bytes, not larger than the bound", len(huge))
	}

	s, stub := sourceSummary(t)
	s.Record = func() string { return huge }
	if _, err := s.summarizeHead(foldedHistory); err != nil {
		t.Fatal(err)
	}
	if got := len(stub.inputs[0]); got > maxNotesInput {
		t.Errorf("the record arm sent %d bytes, over the %d bound", got, maxNotesInput)
	}
	// And the head survives the sampling, which is the half the trial's first
	// prediction rests on: the oldest fact has to be re-readable every fold.
	if !strings.Contains(stub.inputs[0], "RECORD-ONLY-MARKER") {
		t.Error("sampling dropped the record's head, so the oldest fact is not re-readable")
	}
}

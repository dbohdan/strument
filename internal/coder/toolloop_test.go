package coder

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// TestToolLoopWatcherExactRepeat pins the counting rule: three identical
// read-class calls trip the note, and the note is said once — a watcher that
// repeated itself would be a loop detector that loops.
func TestToolLoopWatcherExactRepeat(t *testing.T) {
	w := newToolLoopWatcher()
	args := `{"path":"a.go"}`
	if note := w.observeCall(toolRead, args); note != "" {
		t.Errorf("first call tripped: %q", note)
	}
	if note := w.observeCall(toolRead, args); note != "" {
		t.Errorf("second call tripped: %q", note)
	}
	note := w.observeCall(toolRead, args)
	if note == "" || !strings.Contains(note, toolRead) {
		t.Errorf("third identical call must trip, got %q", note)
	}
	if note2 := w.observeCall(toolRead, args); note2 != "" {
		t.Errorf("the note must fire once, got %q again", note2)
	}
	// Different arguments are a different call: no accumulation across keys.
	w2 := newToolLoopWatcher()
	for range toolLoopMaxIdentical - 1 {
		w2.observeCall(toolGrep, `{"pattern":"a"}`)
	}
	if note := w2.observeCall(toolGrep, `{"pattern":"b"}`); note != "" {
		t.Errorf("different arguments tripped: %q", note)
	}
}

// TestToolLoopWatcherExemptsMutation pins the exemption: bash, check, and
// commit legitimately repeat with unchanged arguments in a working turn
// (lint after every edit), so they never count.
func TestToolLoopWatcherExemptsMutation(t *testing.T) {
	w := newToolLoopWatcher()
	for range 10 {
		if note := w.observeCall(toolBash, `{"command":"task lint"}`); note != "" {
			t.Fatalf("bash tripped: %q", note)
		}
		if note := w.observeCall(toolCheck, `{"name":"lint"}`); note != "" {
			t.Fatalf("check tripped: %q", note)
		}
	}
}

// TestToolLoopWatcherMutationResets pins the salvage of the total counter:
// reads accumulate only since the last mutation. A streak that would have
// tripped is cleared by one edit, and the exact-repeat counts with it — a
// re-read after an edit is a fresh question about a fresh file. This is what
// makes the hardest working turns (dozens of reads, interleaved with edits)
// invisible to a detector that exists for turns where nothing changes.
func TestToolLoopWatcherMutationResets(t *testing.T) {
	w := newToolLoopWatcher()
	for i := range toolLoopMaxReads - 1 {
		// A fresh pattern each call, so only the streak cap is exercised.
		pattern := `{"pattern":"p` + strconv.Itoa(i) + `"}`
		if note := w.observeCall(toolGrep, pattern); note != "" {
			t.Fatalf("read %d tripped early: %q", i+1, note)
		}
	}
	// The next one would trip the streak cap — but an edit lands first.
	w.observeMutation()
	if note := w.observeCall(toolGrep, `{"pattern":"fresh"}`); note != "" {
		t.Errorf("a fresh streak tripped: %q", note)
	}
	// The reset also clears exact-repeat counts: two reads, an edit, two more
	// of the same — legitimate re-reading after progress — must not trip.
	w2 := newToolLoopWatcher()
	args := `{"path":"a.go"}`
	w2.observeCall(toolRead, args)
	w2.observeCall(toolRead, args)
	w2.observeMutation()
	w2.observeCall(toolRead, args)
	if note := w2.observeCall(toolRead, args); note != "" {
		t.Errorf("re-reads after an edit tripped: %q", note)
	}
	if note := w2.observeCall(toolRead, args); note == "" {
		t.Error("three identical reads since the edit must trip")
	}
}

// TestToolLoopWatcherDisabledWithLoopDetection pins the setting: the tool
// watcher obeys loop_detection like the text detector, and a nil watcher
// observes nothing.
func TestToolLoopWatcherDisabledWithLoopDetection(t *testing.T) {
	var w *toolLoopWatcher
	for range 10 {
		if note := w.observeCall(toolRead, `{"path":"a.go"}`); note != "" {
			t.Fatalf("a nil watcher tripped: %q", note)
		}
	}
	c := toolCoder(t, t.TempDir())
	c.LoopDetection = false
	c.initBeforeMessage()
	if c.toolLoops != nil {
		t.Error("loop_detection = false must disable the tool watcher")
	}
}

// TestInterruptToolEndsTheTurn pins the exit: the model's interrupt call
// answers every call, appends the honest harness note after the results, and
// returns the outcome that stops the turn without asking the user "what now".
func TestInterruptToolEndsTheTurn(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	ctx := context.Background()
	c.partialToolCalls = []llm.ToolCall{
		{ID: "call_1", Name: toolRead, Arguments: `{"path":"a.go"}`},
		{ID: "call_2", Name: toolInterrupt, Arguments: `{}`},
	}

	outcome := c.applyToolCalls(ctx)
	if outcome != OutcomeSelfInterrupted {
		t.Fatalf("outcome = %v, want OutcomeSelfInterrupted", outcome)
	}

	// The history ends on the interrupt's note, and the read's result is
	// before it — the call the model made alongside the interrupt still ran.
	last := c.curMessages[len(c.curMessages)-1]
	if !strings.Contains(last.Text(), "The model interrupted the turn") {
		t.Errorf("last message = %q, want the model-interrupt note", last.Text())
	}
	if len(c.curMessages) < 2 {
		t.Fatal("the tool results vanished")
	}
}

// TestToolLoopNoteRidesToolResults pins where the loop note lands: after the
// tool results it describes, as a harness note, in the same send — not a
// synthetic user turn.
func TestToolLoopNoteRidesToolResults(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	ctx := context.Background()
	before := len(c.curMessages)
	args := `{"path":"a.go"}`
	c.partialToolCalls = []llm.ToolCall{
		{ID: "c1", Name: toolRead, Arguments: args},
		{ID: "c2", Name: toolRead, Arguments: args},
		{ID: "c3", Name: toolRead, Arguments: args},
	}
	outcome := c.applyToolCalls(ctx)
	if outcome != OutcomeContinue {
		t.Fatalf("outcome = %v, want OutcomeContinue — the note advises, it does not stop", outcome)
	}
	appended := c.curMessages[before:]
	if len(appended) != 4 { // three results + one note
		t.Fatalf("appended %d messages, want 4", len(appended))
	}
	if !strings.Contains(appended[3].Text(), "same read call") {
		t.Errorf("note = %q, want it to name the repeated tool", appended[3].Text())
	}
}

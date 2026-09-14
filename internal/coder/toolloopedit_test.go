package coder

import (
	"strings"
	"testing"
)

// The field report, reduced: an ambiguous edit resent byte-identically. Before
// this, edit was exempt from the repeat detector on the reasoning that a
// repeated edit self-limits because old_string stops matching -- true of an
// edit that lands, and exactly wrong for one that fails, which changes nothing
// and so fails the same way forever.
func TestRepeatedFailingEditIsNoticed(t *testing.T) {
	w := newToolLoopWatcher()
	args := `{"path":"ci.yml","old_string":"version: '14.3'","new_string":"version: '14.5'"}`

	var note string
	for i := range toolLoopMaxIdentical {
		note = w.observeCall(toolEdit, args)
		if i < toolLoopMaxIdentical-1 && note != "" {
			t.Fatalf("fired after %d identical edits, want %d", i+1, toolLoopMaxIdentical)
		}
	}
	if note == "" {
		t.Fatalf("%d identical failing edits produced no note", toolLoopMaxIdentical)
	}
	// The note has to say the thing the model got wrong: resending it will
	// fail the same way.
	if !strings.Contains(note, "unchanged") {
		t.Errorf("the note does not say resending will not help:\n%s", note)
	}
}

// And the counter-arm, which is what the old exemption was protecting: an edit
// that lands must reset the counters, so ordinary work never trips this.
func TestSuccessfulEditResetsTheWatcher(t *testing.T) {
	w := newToolLoopWatcher()
	args := `{"path":"a.go","old_string":"x","new_string":"y"}`

	w.observeCall(toolEdit, args)
	w.observeCall(toolEdit, args)
	w.observeMutation() // the edits landed
	for range toolLoopMaxIdentical - 1 {
		if note := w.observeCall(toolEdit, args); note != "" {
			t.Fatalf("fired after a mutation reset the count:\n%s", note)
		}
	}
}

// An edit is not an observation. Counting one toward the read cap would make
// the note tell a model it had made read-only calls it never made.
func TestEditsDoNotCountAsReads(t *testing.T) {
	w := newToolLoopWatcher()
	for i := range toolLoopMaxReads + 5 {
		// Vary the arguments so the identical-repeat detector cannot fire and
		// only the read cap is under test.
		note := w.observeCall(toolEdit, `{"path":"a.go","old_string":"`+strings.Repeat("x", i+1)+`"}`)
		if note != "" {
			t.Fatalf("edit %d tripped the read cap:\n%s", i+1, note)
		}
	}
}

// The tools a productive turn genuinely repeats are still exempt: running the
// same tests twice is work, not a loop.
func TestProductiveToolsStayExempt(t *testing.T) {
	for _, name := range []string{toolBash, toolCheck, toolCommit} {
		w := newToolLoopWatcher()
		for range toolLoopMaxIdentical + 2 {
			if note := w.observeCall(name, `{"command":"go test ./..."}`); note != "" {
				t.Errorf("%s fired on repeated identical calls:\n%s", name, note)
			}
		}
	}
}

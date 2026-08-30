// A panic mid-turn must not take the turn's record with it.
//
// The transcript lives outside the Coder and is appended after Run returns, so
// a turn that panics never reaches it: the tool calls it made, the files it
// edited, and its partial answer are lost with the process. Run recovers once,
// hands the partial answer to OnCrash, and re-panics — dying is still the
// outcome (a recovered panic in a coding agent leaves the tree half-edited and
// the session running on a broken coder), but the work survives in the
// transcript via the caller's callback.

package coder

import (
	"context"
	"testing"

	"dbohdan.com/strument/internal/fixture"
)

// TestRunOnCrashReceivesPartialAnswer pins the crash contract: a panic inside
// the turn reaches OnCrash with what the turn had produced, and the panic is
// re-raised — recovery records, it does not swallow.
func TestRunOnCrashReceivesPartialAnswer(t *testing.T) {
	c := askCoder(t, t.TempDir())
	var crashedWith string
	c.OnCrash = func(partial string) { crashedWith = partial }

	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{{
		Events: []fixture.Event{
			{Kind: "Answer", Text: "halfway through the work"},
			{Kind: "Panic", Message: "boom"},
		},
	}}}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Run should re-panic after OnCrash, not swallow the panic")
		}
		if crashedWith != "halfway through the work" {
			t.Errorf("OnCrash partial answer = %q, want the streamed prefix", crashedWith)
		}
	}()

	c.Run(context.Background(), "do the thing")
	t.Fatal("Run returned without panicking")
}

// TestRunOnCrashNilRecordsNothing keeps the default: without a callback, a
// panic behaves exactly as before — it simply propagates.
func TestRunOnCrashNilRecordsNothing(t *testing.T) {
	c := askCoder(t, t.TempDir())
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{{
		Events: []fixture.Event{
			{Kind: "Answer", Text: "started"},
			{Kind: "Panic", Message: "boom"},
		},
	}}}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Run should propagate the panic")
		}
	}()
	c.Run(context.Background(), "do the thing")
}

// TestPanicUnwindsThroughTurnDefer pins the ordering the crash path relies on:
// OnCrash runs after runOne's turn-end defer, so the accessors the transcript
// writer reads — TurnEditedFiles, TurnToolLines — report the turn's settled
// state, and endTurnHistory has already settled the conversation.
func TestPanicUnwindsThroughTurnDefer(t *testing.T) {
	dir := t.TempDir()
	c := askCoder(t, dir)
	c.AddFile("main.txt")

	var curLen, doneLen int
	c.OnCrash = func(_ string) {
		// endTurnHistory (runOne's defer) moves cur into done; it must have
		// run before the crash callback, or the transcript would record a
		// conversation the coder no longer holds.
		curLen, doneLen = len(c.curMessages), len(c.doneMessages)
	}

	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{{
		Events: []fixture.Event{
			{Kind: "Answer", Text: "halfway through the work"},
			{Kind: "Panic", Message: "boom"},
		},
	}}}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected the panic to propagate")
			}
		}()
		c.Run(context.Background(), "do the thing")
	}()

	if doneLen == 0 || curLen != 0 {
		t.Errorf("after the crash callback: %d cur, %d done; want the turn's messages settled into done", curLen, doneLen)
	}
}

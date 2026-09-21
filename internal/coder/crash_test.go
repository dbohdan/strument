// A panic mid-turn must not take the turn's record with it.
//
// The turn record is emitted by runOne's own defer, so a panic unwinding
// through it is recorded on the way out: the tool calls the turn made, the
// files it edited, and the partial answer it had produced. The panic is then
// re-raised — dying is still the outcome, because a recovered panic in a
// coding agent leaves the tree half-edited and the session running on a broken
// coder — but the work is on disk before the process goes.
//
// This used to run through an OnCrash callback into a transcript writer that
// lived outside the Coder, because the transcript was appended after Run
// returned and a turn that never returned never reached it. The record has no
// such gap.

package coder

import (
	"context"
	"testing"

	"dbohdan.com/strument/internal/fixture"
)

// crashingTurn is the last send of a turn: some prose, then a panic.
var crashingTurn = fixture.Turn{Events: []fixture.Event{
	{Kind: "Answer", Text: "halfway through the work"},
	{Kind: "Panic", Message: "boom"},
}}

// crashRecord runs a turn that ends in a panic and returns the turn row it
// left behind.
func crashRecord(t *testing.T, c *Coder, turns ...fixture.Turn) Record {
	t.Helper()
	rec := &capture{}
	c.Recorder = rec
	c.Client = &fixture.StreamStub{Turns: append(turns, crashingTurn)}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("the panic must propagate; recording is not recovering")
			}
		}()
		c.Run(context.Background(), "do the thing")
	}()

	for _, r := range rec.recs {
		if r.Type == "turn" {
			return r
		}
	}
	t.Fatal("a turn that crashed left no turn record")
	return Record{}
}

// The crash contract: what the turn had produced reaches the record, marked as
// the fragment it is, and the panic is re-raised.
func TestACrashedTurnIsRecordedWithItsPartialAnswer(t *testing.T) {
	r := crashRecord(t, askCoder(t, t.TempDir()))

	if r.Outcome != OutcomeCrashed {
		t.Errorf("outcome = %q, want %q", r.Outcome, OutcomeCrashed)
	}
	if r.Answer != "halfway through the work" {
		t.Errorf("answer = %q, want the streamed prefix", r.Answer)
	}
	if r.Prompt != "do the thing" {
		t.Errorf("prompt = %q, want the message that opened the turn", r.Prompt)
	}
}

// A turn that ends normally is not marked as a crash. Without this the check
// above passes on a constant.
func TestANormalTurnIsNotMarkedCrashed(t *testing.T) {
	c := testCoder(t)
	rec := &capture{}
	c.Recorder = rec
	c.Client = &toolThenAnswer{}

	c.runOne(context.Background(), "do the thing")

	for _, r := range rec.recs {
		if r.Type == "turn" && r.Outcome == OutcomeCrashed {
			t.Error("a turn that returned normally was recorded as a crash")
		}
	}
}

// The record is emitted from inside the turn-end defer, so it sees the turn's
// settled state — the files it changed and the lines it printed — rather than
// whatever was half-assigned when the panic fired.
func TestACrashedTurnRecordsTheWorkItHadDone(t *testing.T) {
	c := askCoder(t, t.TempDir())

	// One step's worth of work, then the crash. The tool line it prints is
	// recorded by the tee that initBeforeMessage resets, so a line printed
	// before the turn would not count — it has to happen inside one.
	r := crashRecord(t, c, fixture.Turn{Events: []fixture.Event{
		{Kind: "ToolCall", ToolIndex: 0, ToolID: "call_1", ToolName: "ls", ToolArgs: `{"path":"."}`},
		{Kind: "Finish", FinishReason: "tool_calls"},
	}})

	if len(r.Tools) == 0 {
		t.Error("the crashed turn recorded no tool lines; the harness printed one")
	}
	// The conversation is settled by the same defer: a record naming messages
	// the coder no longer holds would be a record of a state that never was.
	if len(c.curMessages) != 0 || len(c.doneMessages) == 0 {
		t.Errorf("after the crash: %d cur, %d done; want the turn's messages settled into done",
			len(c.curMessages), len(c.doneMessages))
	}
}

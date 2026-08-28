// Steering: what an interrupted turn does next, and what it settles on the way.

package coder

import (
	"context"
	"iter"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// stubAsker answers with a fixed line, recording the questions it was asked.
type stubAsker struct {
	answer string
	asked  []string
}

func (a *stubAsker) Ask(req AskRequest) []string {
	a.asked = append(a.asked, req.Question)
	if a.answer == "" {
		return nil
	}
	return ParseAskAnswer(req, a.answer)
}

// cancelOnceClient interrupts the first send and answers normally after that,
// which is the shape of a turn the user stopped and then let continue.
type cancelOnceClient struct {
	sends    int
	requests [][]llm.Message
}

func (c *cancelOnceClient) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	c.requests = append(c.requests, req.Messages)
	n := c.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		if n == 1 {
			if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Starting on"}, nil) {
				return
			}
			yield(llm.StreamEvent{}, context.Canceled)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "All done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func steerCoder(t *testing.T, answer string) (*Coder, *cancelOnceClient, *stubAsker) {
	t.Helper()
	c := testCoder(t)
	client := &cancelOnceClient{}
	asker := &stubAsker{answer: answer}
	c.Client = client
	c.Asker = asker
	return c, client, asker
}

// "Continue" resumes the same turn rather than ending it.
//
// The whole point of the per-send context: cancelling used to cancel the
// *turn's* context, so every later call in it saw Canceled and there was no way
// back into the loop even though the conversation had survived intact.
func TestSteerContinueResumesTheTurn(t *testing.T) {
	c, client, asker := steerCoder(t, "1") // "Continue"

	c.runOne(context.Background(), "do the thing")

	if len(asker.asked) != 1 {
		t.Fatalf("asked %d questions, want 1", len(asker.asked))
	}
	if client.sends != 2 {
		t.Errorf("sends = %d, want 2 — the turn stopped instead of continuing", client.sends)
	}
	if c.lastSendOutcome != OutcomeSuccess {
		t.Errorf("last outcome = %v, want the resumed send to finish", c.lastSendOutcome)
	}
}

// Continuing must not put an empty user turn on the wire.
//
// "Continue" has no message: the note left by the interrupt is the message. A
// send with empty text still appends a user turn unless told the message is
// already there, which is what resumeInPlace is for — the same mechanism a tool
// continuation uses. Without it the model receives a blank turn from the user
// and has to guess what it meant.
func TestSteerContinueSendsNoEmptyTurn(t *testing.T) {
	c, client, _ := steerCoder(t, "1") // "Continue"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2", client.sends)
	}
	for i, m := range client.requests[1] {
		if m.Role == llm.RoleUser && m.Text() == "" {
			t.Errorf("message %d is an empty user turn", i)
		}
	}
	// The note is what the resumed send carries in its place.
	var noted bool
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			noted = true
		}
	}
	if !noted {
		t.Error("the resumed send carries no interrupt note")
	}
}

// A typed correction goes in as the user's own words and reaches the model.
func TestSteerCorrectionReachesTheModel(t *testing.T) {
	correction := "use the standard library, not a regexp"
	c, client, _ := steerCoder(t, correction)

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2", client.sends)
	}
	var found bool
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleUser && m.Text() == correction {
			found = true
		}
	}
	if !found {
		t.Errorf("the correction never reached the model as a user turn")
	}
	// In the user's voice, unmarked: they really did type it.
	for _, m := range client.requests[1] {
		if m.Text() == llm.HarnessMarker+" "+correction {
			t.Errorf("the correction was marked as the harness speaking")
		}
	}
}

// "Stop" ends the turn, which is what an interrupt has always done.
func TestSteerStopEndsTheTurn(t *testing.T) {
	c, client, _ := steerCoder(t, "2") // "Stop"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 1 {
		t.Errorf("sends = %d, want 1 — Stop should not resume", client.sends)
	}
	if c.lastSendOutcome != OutcomeInterrupted {
		t.Errorf("last outcome = %v, want OutcomeInterrupted", c.lastSendOutcome)
	}
}

// No terminal to ask on means stop, silently and without a prompt.
//
// Script mode has a nil Asker, and stopping there is both the honest answer and
// exactly what an interrupt did before any of this existed.
func TestSteerWithoutAnAskerStops(t *testing.T) {
	c := testCoder(t)
	client := &cancelOnceClient{}
	c.Client = client
	c.Asker = nil

	c.runOne(context.Background(), "do the thing")

	if client.sends != 1 {
		t.Errorf("sends = %d, want 1 — a nil Asker must not resume", client.sends)
	}
}

// An interrupt settles the edits made before it, and settling twice over the
// same edits does nothing the second time.
//
// The interruption is a review boundary: the commit and the snapshot at that
// point are what let `git show` and /undo separate what the model did before
// the correction from what it did after. The idempotence half matters just as
// much — the turn-end defer settles too, and a second pass over unchanged edits
// used to announce "the turn left the files as they were", which is true of the
// second attempt and false of the turn.
func TestSettleEditsIsIdempotent(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "after")

	c.settleEdits("")
	if got := len(c.undoStack); got != 1 {
		t.Fatalf("undo stack = %d after the first settle, want 1", got)
	}

	c.settleEdits("")
	if got := len(c.undoStack); got != 1 {
		t.Errorf("undo stack = %d after settling again, want 1 — /undo would need two presses", got)
	}
}

// Continuing must tell the model it was asked to continue.
//
// This is the check the other Continue tests should have been. They assert a
// second send went out and that *a* harness note is present — both true of the
// broken version, where the only note said "your reply was cut off" and nothing
// said what the user decided. The model read that as a full stop and stopped,
// which was the correct reading of what it was actually sent.
//
// So this asserts on content, and on the *last* message, because position is
// the half that matters: an instruction to resume is only an instruction to
// resume if it is the thing the model is answering.
func TestSteerContinueSaysToContinue(t *testing.T) {
	c, client, _ := steerCoder(t, "1") // "Continue"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2", client.sends)
	}
	resumed := client.requests[1]
	last := resumed[len(resumed)-1]

	if last.Role != llm.RoleUser || !strings.HasPrefix(last.Text(), llm.HarnessMarker) {
		t.Fatalf("the resumed send ends on a %s message, want the harness's note: %q",
			last.Role, last.Text())
	}
	text := strings.ToLower(last.Text())
	if !strings.Contains(text, "continue") {
		t.Errorf("the note never says the user chose to continue:\n%s", last.Text())
	}
	// Both failure modes seen live were the model doing something reasonable
	// with a note that did not rule them out: it stopped and asked what next,
	// or it restarted from the top. The note has to close both.
	for _, want := range []string{"start over", "repeat"} {
		if !strings.Contains(text, want) {
			t.Errorf("the note does not rule out restarting (%q missing):\n%s", want, last.Text())
		}
	}
}

// A second commit in one turn must not name the first commit's files.
//
// commitTurn used to pass every file the *turn* had touched, because the
// end-of-turn history record wants that set and, while a turn made one commit,
// the two were the same thing. Steering made them different: an
// interrupted-then-steered turn commits twice, and the second call was handing
// git the first commit's paths as well.
//
// Git only commits what actually differs, so this is invisible until someone
// edits one of those files themselves between the two commits — at which point
// their work is swept into a model-authored commit. A tool that makes multiple
// commits routine turns a latent bug into a regular one.
func TestSecondCommitNamesOnlyWhatIsNew(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	repo := &countingRepo{}
	c.AutoCommits = true
	c.Repo = repo

	// First batch: a.go. Settle it.
	c.turnEditedFiles["a.go"] = true
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one")
	c.settleEdits("")

	// Second batch: b.go only. turnEditedFiles still holds both, because the
	// turn's history record wants every file the turn touched.
	c.turnEditedFiles["b.go"] = true
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("b.go", snapEntry{}, "two")
	c.settleEdits("")

	staged := repo.calls
	if len(staged) != 2 {
		t.Fatalf("%d commits, want 2", len(staged))
	}
	if slices.Contains(staged[1], "a.go") {
		t.Errorf("the second commit names the first commit's file: %v", staged[1])
	}
	if !slices.Contains(staged[1], "b.go") {
		t.Errorf("the second commit does not name what it changed: %v", staged[1])
	}
	// The turn-wide set is still whole, for the history record.
	if got := len(c.TurnEditedFiles()); got != 2 {
		t.Errorf("TurnEditedFiles() = %d, want both files kept for the record", got)
	}
}

// cancellingRunner cancels the turn while the command is running, which is
// what a Ctrl-C during a test run does.
type cancellingRunner struct {
	cancel func()
	ran    int
}

func (r *cancellingRunner) Run(ctx context.Context, _ string, _ string) (int, string, error) {
	r.ran++
	r.cancel()
	<-ctx.Done()
	return -1, "partial output before the interrupt", nil
}

// A Ctrl-C while a command is running reaches the steer menu.
//
// sendMessage decides "interrupted" from how the stream ended, and tool calls
// run after the stream — so this used to kill the command and return
// OutcomeContinue. The turn carried on, the menu never appeared, and a second
// press inside the chord window quit Strument. Every live interrupt tested so
// far caught prose, which is why none of them found it.
func TestInterruptDuringAToolReachesTheMenu(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	asker := &stubAsker{answer: "2"} // "Stop"
	c.Asker = asker
	c.SuggestShellCommands = true
	c.Confirm = yesConfirmer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &cancellingRunner{cancel: cancel}
	c.Runner = runner
	c.partialToolCalls = []llm.ToolCall{
		{ID: "call_1", Name: toolBash, Arguments: `{"command":"go test ./...","purpose":"run the tests"}`},
	}

	outcome := c.applyToolCalls(ctx)

	if runner.ran != 1 {
		t.Fatalf("the command ran %d times, want 1", runner.ran)
	}
	if outcome != OutcomeInterrupted {
		t.Errorf("outcome = %v, want OutcomeInterrupted so runOne asks what the user meant", outcome)
	}
}

// ...and the model is told the command was stopped rather than left to read a
// truncated result as a failure.
func TestInterruptDuringAToolTellsTheModel(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.SuggestShellCommands = true
	c.Confirm = yesConfirmer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Runner = &cancellingRunner{cancel: cancel}
	c.partialToolCalls = []llm.ToolCall{
		{ID: "call_1", Name: toolBash, Arguments: `{"command":"go test ./...","purpose":"run the tests"}`},
	}

	c.applyToolCalls(ctx)

	var sawResult, sawNote bool
	for _, m := range c.curMessages {
		if m.Role == llm.RoleTool && strings.Contains(m.Text(), "pressed Ctrl-C") {
			sawResult = true
		}
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			sawNote = true
		}
	}
	if !sawResult {
		t.Error("the command's own output does not say the user stopped it")
	}
	if !sawNote {
		t.Error("no harness note explaining the interruption")
	}
	// Every call still has an answer: an interrupt here must not leave the
	// next request malformed, which is the failure the streaming path avoids
	// by dropping partial calls instead.
	for _, m := range c.curMessages {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			t.Error("tool calls were dropped; here they ran and were answered")
		}
	}
}

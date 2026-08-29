// Degenerate repetition: what counts as a loop, and what the turn does about it.

package coder

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// prose returns n distinct sentences, as ordinary output that must not trip
// anything. Distinct by construction rather than by hand: a hand-written
// paragraph is short enough that it passes for the wrong reason.
func prose(n int) string {
	// Every sentence differs in the middle and at the end. An earlier version
	// varied only the opening, which put a constant fifty-character tail in
	// every line — and the detector was right to call that a loop.
	subjects := []string{"parser", "scanner", "encoder", "buffer", "walker", "matcher", "writer"}
	verbs := []string{"reads", "rejects", "widens", "counts", "folds", "skips", "hashes"}
	objects := []string{"the header", "a stray byte", "the length field", "each rune", "the tail", "one frame"}
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "Step %d: the %s %s %s before %s %s does.\n",
			i, subjects[i%7], verbs[(i*3)%7], objects[(i*5)%6],
			subjects[(i*2+1)%7], verbs[(i*4+2)%7])
	}
	return b.String()
}

func TestFindLoopCatchesARepeatedSentence(t *testing.T) {
	text := strings.Repeat("I need to check the file again to be sure of the contents. ", 12)

	f := findLoop(text, loopMinCount)
	if f == nil {
		t.Fatal("no finding for a sentence repeated 12 times")
	}
	if f.Count < loopMinCount {
		t.Errorf("count = %d, want at least %d", f.Count, loopMinCount)
	}
	if !strings.Contains(f.Sample, "check the file again") {
		t.Errorf("sample = %q, want the repeating sentence", f.Sample)
	}
}

func TestFindLoopIgnoresOrdinaryProse(t *testing.T) {
	if f := findLoop(prose(200), loopMinCount); f != nil {
		t.Errorf("prose reported as a loop: %q ×%d", f.Sample, f.Count)
	}
}

// Spacing is what separates a loop from a habit of phrasing. The same sentence
// ten times over, with a page of other work between each, is a model that likes
// a sentence — not one that is stuck.
func TestFindLoopIgnoresWidelySpacedRepeats(t *testing.T) {
	unit := "I need to check the file again to be sure of the contents. "
	var b strings.Builder
	for range loopMinCount + 2 {
		b.WriteString(unit)
		b.WriteString(prose(30)) // well over loopMaxAvgGap between occurrences
	}

	if f := findLoop(b.String(), loopMinCount); f != nil {
		t.Errorf("widely spaced repeats reported as a loop: %q ×%d", f.Sample, f.Count)
	}
}

func TestFindLoopIgnoresOutliersAroundLoop(t *testing.T) {
	unit := "I need to check the file again to be sure of the contents. "
	loop := strings.Repeat(unit, loopMinCount)
	separator := strings.Repeat("x", loopMaxGap)

	for _, text := range []string{
		unit + separator + loop,
		loop + separator + unit,
	} {
		if f := findLoop(text, loopMinCount); f == nil {
			t.Errorf("missed loop next to an isolated occurrence")
		}
	}
}

// Whitespace recurs forever and means nothing. A blank-line run is what a model
// emits while formatting, not while looping.
func TestFindLoopIgnoresWhitespace(t *testing.T) {
	if f := findLoop(strings.Repeat("\n", 4000), loopMinCount); f != nil {
		t.Errorf("blank lines reported as a loop: %q ×%d", f.Sample, f.Count)
	}
}

// The stutter the window detector cannot see: one word, no sentence, no
// terminator. "Dynamical" 84 times was a real reply.
func TestFindWordRunCatchesAStutter(t *testing.T) {
	f := findWordRun("The system is Dynamical "+strings.Repeat("Dynamical ", 40), loopMinWordRun)
	if f == nil {
		t.Fatal("no finding for a word repeated 40 times")
	}
	if f.Sample != "Dynamical" {
		t.Errorf("sample = %q, want the repeated word", f.Sample)
	}
}

// A row of zeroes is a matrix, not a stutter.
func TestFindWordRunIgnoresRepeatedNumbers(t *testing.T) {
	if f := findWordRun(strings.Repeat("0 ", loopMinWordRun+10), loopMinWordRun); f != nil {
		t.Errorf("a row of zeroes reported as a loop: %q ×%d", f.Sample, f.Count)
	}
}

// Answer and reasoning are separate texts. They interleave on the wire, and a
// window spanning the seam is not a repetition of anything the model wrote.
func TestLoopDetectorKeepsTheStreamsApart(t *testing.T) {
	d := newLoopDetector(true)
	unit := "I need to check the file again to be sure of the contents. "
	// Nearly twice loopMinCount overall, but the occurrences alternate, so
	// neither stream holds enough of them on its own.
	for i := range 2*loopMinCount - 2 {
		kind := loopAnswer
		if i%2 == 1 {
			kind = loopReasoning
		}
		if f := d.feed(kind, unit); f != nil {
			t.Fatalf("fired on the %s stream at %q", kind, f.Sample)
		}
	}
}

// Feeding it in fragments is the real case: a loop arrives a token at a time.
func TestLoopDetectorFiresOnFragments(t *testing.T) {
	d := newLoopDetector(true)
	unit := "I need to check the file again to be sure of the contents. "
	var found *loopFinding
	for range loopMinCount + 2 {
		for _, word := range strings.SplitAfter(unit, " ") {
			if f := d.feed(loopReasoning, word); f != nil && found == nil {
				found = f
			}
		}
	}
	if found == nil {
		t.Fatal("no finding for a loop streamed word by word")
	}
	if found.Kind != loopReasoning {
		t.Errorf("kind = %q, want %q", found.Kind, loopReasoning)
	}
	if d.Found == nil || d.Found.Sample != found.Sample {
		t.Error("the first finding was not kept for the caller to quote")
	}
}

// `loop_detection = False` means exactly that.
func TestLoopDetectorOffNeverFires(t *testing.T) {
	d := newLoopDetector(false)
	for range 40 {
		if f := d.feed(loopReasoning, strings.Repeat("Dynamical ", 4)); f != nil {
			t.Fatalf("a disabled detector fired: %q ×%d", f.Sample, f.Count)
		}
	}
	// And a nil one, which is what a side call passes.
	var nilDet *loopDetector
	if f := nilDet.feed(loopReasoning, strings.Repeat("Dynamical ", 100)); f != nil {
		t.Errorf("a nil detector fired: %q", f.Sample)
	}
}

// The tail is bounded, so a very long reply cannot grow the map without limit —
// and a loop still running is still caught inside it.
func TestLoopDetectorBoundsWhatItKeeps(t *testing.T) {
	d := newLoopDetector(true)
	for range 20 {
		d.feed(loopAnswer, prose(30))
	}
	if got := d.answer.kept.Len(); got > loopTailBytes {
		t.Errorf("kept %d bytes, want at most %d", got, loopTailBytes)
	}
	var found bool
	for range loopMinCount + 2 {
		if f := d.feed(loopAnswer, "I need to check the file again to be sure of the contents. "); f != nil {
			found = true
		}
	}
	if !found {
		t.Error("a loop that started after the trim was missed")
	}
}

func TestLoopDetectorBoundsAnUnterminatedLine(t *testing.T) {
	var s loopStream
	s.add(strings.Repeat("Dynamical ", loopTailBytes))

	if got := s.kept.Len() + len(s.pending); got > loopTailBytes {
		t.Errorf("tail %d bytes, want at most %d", got, loopTailBytes)
	}
	if f := findWordRun(s.tail(), loopMinWordRun); f == nil {
		t.Error("a stutter in the bounded unterminated line was missed")
	}
}

// loopingClient repeats one sentence forever on the first send and answers
// normally afterwards. Forever is the point: nothing but the detector ends it,
// so a test that hangs here is a detector that does not work.
type loopingClient struct {
	sends    int
	requests [][]llm.Message
	kind     llm.EventKind
	// repeats bounds the first reply, for the one test that has to survive a
	// detector that does nothing. Zero is unbounded.
	repeats int
}

func (c *loopingClient) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	c.sends++
	c.requests = append(c.requests, req.Messages)
	n := c.sends
	return func(yield func(llm.StreamEvent, error) bool) {
		if n == 1 {
			for i := 0; c.repeats == 0 || i < c.repeats; i++ {
				ev := llm.StreamEvent{Kind: c.kind, Text: "I need to check the file again to be sure. "}
				if !yield(ev, nil) {
					return
				}
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "All done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func loopCoder(t *testing.T, kind llm.EventKind, answer string) (*Coder, *loopingClient, *stubAsker) {
	t.Helper()
	c := testCoder(t)
	client := &loopingClient{kind: kind}
	asker := &stubAsker{answer: answer}
	c.Client = client
	c.Asker = asker
	return c, client, asker
}

// End to end: a looping reply stops, and the turn asks what to do about it.
func TestLoopingReplyStopsTheSend(t *testing.T) {
	c, client, asker := loopCoder(t, llm.EventReasoning, "1") // "Stop"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 1 {
		t.Errorf("sends = %d, want 1 — Stop should not resume", client.sends)
	}
	if c.lastSendOutcome != OutcomeLooping {
		t.Errorf("last outcome = %v, want OutcomeLooping", c.lastSendOutcome)
	}
	if len(asker.asked) != 1 {
		t.Fatalf("asked %d questions, want 1", len(asker.asked))
	}
	// Not "you stopped the model": the user did not.
	if strings.Contains(strings.ToLower(asker.asked[0]), "you stopped") {
		t.Errorf("the question credits the user with the stop: %q", asker.asked[0])
	}
}

// The note the model reads next is the harness speaking, and it says what
// repeated. Told only "you were repeating yourself", a model has to guess which
// part, and the guess is usually the whole reply.
func TestLoopNoteIsMarkedAndQuotesTheRepeat(t *testing.T) {
	c, client, _ := loopCoder(t, llm.EventReasoning, "2") // "Try again"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2 — Try again should re-send", client.sends)
	}
	var note string
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			note = m.Text()
		}
	}
	if note == "" {
		t.Fatal("the resumed send carries no loop note")
	}
	if !strings.Contains(note, "check the file again") {
		t.Errorf("the note does not quote what repeated: %q", note)
	}
	if !strings.Contains(note, "reasoning") {
		t.Errorf("the note does not say which stream repeated: %q", note)
	}
	// The resumed send must not carry an empty user turn in place of a message.
	for i, m := range client.requests[1] {
		if m.Role == llm.RoleUser && m.Text() == "" {
			t.Errorf("message %d is an empty user turn", i)
		}
	}
}

// A loop in the answer text is caught too, and the partial the user saw stays
// in the history: it is real output, and dropping it would leave the note
// referring to nothing.
func TestLoopingAnswerKeepsThePartial(t *testing.T) {
	c, client, _ := loopCoder(t, llm.EventAnswer, "2") // "Try again"

	c.runOne(context.Background(), "do the thing")

	if client.sends != 2 {
		t.Fatalf("sends = %d, want 2", client.sends)
	}
	var partial string
	for _, m := range client.requests[1] {
		if m.Role == llm.RoleAssistant {
			partial = m.Text()
		}
	}
	if !strings.Contains(partial, "check the file again") {
		t.Errorf("the partial reply was dropped: %q", partial)
	}
}

// Turning it off means the loop is not stopped. The counter-test to all of the
// above: a setting that does nothing would pass every one of them.
//
// The client is bounded here, because with the detector off nothing else would
// ever end the reply — which is the whole point of the setting being a choice.
func TestLoopDetectionOffLetsTheLoopRun(t *testing.T) {
	c, client, asker := loopCoder(t, llm.EventReasoning, "1")
	client.repeats = 200
	c.LoopDetection = false

	c.runOne(context.Background(), "do the thing")

	if c.lastSendOutcome == OutcomeLooping {
		t.Error("a disabled detector still stopped the reply")
	}
	if len(asker.asked) != 0 {
		t.Errorf("asked %d questions with detection off, want 0", len(asker.asked))
	}
	// And the same reply, with detection on, is stopped: the pair is what makes
	// either half mean anything.
	on, onClient, _ := loopCoder(t, llm.EventReasoning, "1")
	onClient.repeats = 200
	on.runOne(context.Background(), "do the thing")
	if on.lastSendOutcome != OutcomeLooping {
		t.Errorf("last outcome = %v with detection on, want OutcomeLooping", on.lastSendOutcome)
	}
}

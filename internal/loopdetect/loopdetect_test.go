package loopdetect

import (
	"fmt"
	"strings"
	"testing"
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
	d := New(true)
	unit := "I need to check the file again to be sure of the contents. "
	// Nearly twice loopMinCount overall, but the occurrences alternate, so
	// neither stream holds enough of them on its own.
	for i := range 2*loopMinCount - 2 {
		kind := Answer
		if i%2 == 1 {
			kind = Reasoning
		}
		if f := d.Feed(kind, unit); f != nil {
			t.Fatalf("fired on the %s stream at %q", kind, f.Sample)
		}
	}
}

// Feeding it in fragments is the real case: a loop arrives a token at a time.
func TestLoopDetectorFiresOnFragments(t *testing.T) {
	d := New(true)
	unit := "I need to check the file again to be sure of the contents. "
	var found *Finding
	for range loopMinCount + 2 {
		for _, word := range strings.SplitAfter(unit, " ") {
			if f := d.Feed(Reasoning, word); f != nil && found == nil {
				found = f
			}
		}
	}
	if found == nil {
		t.Fatal("no finding for a loop streamed word by word")
	}
	if found.Kind != Reasoning {
		t.Errorf("kind = %q, want %q", found.Kind, Reasoning)
	}
	if d.Found == nil || d.Found.Sample != found.Sample {
		t.Error("the first finding was not kept for the caller to quote")
	}
}

// `loop_detection = False` means exactly that.
func TestLoopDetectorOffNeverFires(t *testing.T) {
	d := New(false)
	for range 40 {
		if f := d.Feed(Reasoning, strings.Repeat("Dynamical ", 4)); f != nil {
			t.Fatalf("a disabled detector fired: %q ×%d", f.Sample, f.Count)
		}
	}
	// And a nil one, which is what a side call passes.
	var nilDet *Detector
	if f := nilDet.Feed(Reasoning, strings.Repeat("Dynamical ", 100)); f != nil {
		t.Errorf("a nil detector fired: %q", f.Sample)
	}
}

// The tail is bounded, so a very long reply cannot grow the map without limit —
// and a loop still running is still caught inside it.
func TestLoopDetectorBoundsWhatItKeeps(t *testing.T) {
	d := New(true)
	for range 20 {
		d.Feed(Answer, prose(30))
	}
	if got := d.answer.kept.Len(); got > loopTailBytes {
		t.Errorf("kept %d bytes, want at most %d", got, loopTailBytes)
	}
	var found bool
	for range loopMinCount + 2 {
		if f := d.Feed(Answer, "I need to check the file again to be sure of the contents. "); f != nil {
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

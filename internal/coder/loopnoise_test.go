package coder

import (
	"fmt"
	"strings"
	"testing"
)

// The detector must keep catching loops while it stops firing on quoted
// material. Both halves are in one table so neither can be improved at the
// other's expense without the table saying so.
//
// The false positive this fixes was live: two of thirty runs in the skills
// trial were stopped mid-turn for quoting the file they had been asked to
// edit. The loop shapes below are the ones the detector was built on -- a
// repeated sentence, and the one-word-per-line stutter that an over-broad
// version of this very strip once deleted.

// gridBlock is the fixture that actually tripped the detector: thirteen
// near-identical SVG elements. The repeating 50-byte window straddles the line
// break, which is why "the lines all differ" does not save it.
func gridBlock() string {
	var b strings.Builder
	for _, y := range []string{"40.0", "100.0", "160.0", "220.0", "280.0", "340.0"} {
		fmt.Fprintf(&b, `  <line class="grid" x1="60" y1="%s" x2="690" y2="%s" stroke="#999999" stroke-width="1"/>`+"\n", y, y)
	}
	for _, x := range []string{"60.0", "165.0", "270.0", "375.0", "480.0", "585.0", "690.0"} {
		fmt.Fprintf(&b, `  <line class="grid" x1="%s" y1="40" x2="%s" y2="340" stroke="#999999" stroke-width="1"/>`+"\n", x, x)
	}
	return b.String()
}

func jsonBlock() string {
	var b strings.Builder
	b.WriteString("[\n")
	for i := range 14 {
		fmt.Fprintf(&b, "  {\n    \"identifier\": \"widget-%02d\",\n    \"enabled\": true,\n"+
			"    \"description\": \"a widget for the thing\",\n    \"weight\": %d\n  },\n", i, i*3)
	}
	b.WriteString("]\n")
	return b.String()
}

func tableBlock() string {
	var b strings.Builder
	b.WriteString("| model | input $/M | output $/M | context | notes |\n" +
		"| --- | --- | --- | --- | --- |\n")
	for i := range 16 {
		fmt.Fprintf(&b, "| model-%02d | 0.140 | 0.280 | 262144 | nothing to say here |\n", i)
	}
	return b.String()
}

func fenced(s string) string { return "Here is the file:\n\n```html\n" + s + "```\n" }

func feedAll(t *testing.T, kind, text string) *loopFinding {
	t.Helper()
	d := newLoopDetector(true)
	var first *loopFinding
	// A token at a time, which is how it really arrives: a per-line feed would
	// hide any bug in the partial-line handling.
	for _, chunk := range strings.SplitAfter(text, " ") {
		if f := d.feed(kind, chunk); f != nil && first == nil {
			first = f
		}
	}
	return first
}

func TestLoopDetectorSeparatesLoopsFromQuotedMaterial(t *testing.T) {
	sentence := strings.Repeat("I need to check the file again to be sure of the contents. ", loopMinCount+2)
	stutter := strings.Repeat("Dynamical\n", 84)
	bangs := strings.Repeat("!", 2*loopWindow*loopMinCount)

	for _, test := range []struct {
		name string
		text string
		want bool // must a loop be reported?
	}{
		// Real loops. Every one of these must still fire.
		{"repeated sentence", sentence, true},
		{"one word per line stutter", stutter, true},
		{"a screenful of punctuation", bangs, true},
		{"a loop inside a fence still counts as text around it",
			"Thinking.\n" + sentence, true},

		// Quoted material. None of these may fire.
		{"fenced svg", fenced(gridBlock()), false},
		{"unfenced svg", "Current file:\n\n" + gridBlock() + "\nThat is the chart.\n", false},
		{"fenced json", fenced(jsonBlock()), false},
		{"unfenced json", jsonBlock(), false},
		{"markdown table", tableBlock(), false},
		{"svg quoted twice, as an edit's two sides",
			"old:\n```\n" + gridBlock() + "```\nnew:\n```\n" + gridBlock() + "```\n", false},
		{"prose around quoted markup",
			"I will restyle it.\n\n```\n" + gridBlock() + "```\n\nThat removes the vertical lines.\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := feedAll(t, loopReasoning, test.text)
			if test.want && got == nil {
				t.Errorf("no loop reported; this shape is a real loop")
			}
			if !test.want {
				// The fixture must be able to trip an unstripped detector, or
				// it proves nothing and would sit here looking like a passing
				// regression test forever. Three of these did exactly that
				// until the counter-arm was read line by line.
				if findLoop(test.text, loopMinCount) == nil {
					t.Errorf("fixture cannot trip the detector even unstripped; "+
						"it is decorative, not a regression test (%d bytes)", len(test.text))
				}
				if got != nil {
					t.Errorf("fired on quoted material: %q ×%d", got.Sample, got.Count)
				}
			}
		})
	}
}

// quotedMessage is the fixture for the second live false positive, the one
// loop-false-positive.log captured: a model doing a message-shortening pass
// quotes the same user-facing string each time it weighs it, with a different
// length of deliberation between quotes. Every line is ordinary prose -- the
// strip passes all of it -- and ten quotes put a 50-byte window over
// loopMinCount, which is why the average-gap rule fired. What saves it is the
// spacing: the gaps swing by more than loopGapJitter between one line of
// "let me reconsider" and three. An actual loop repeats at its period, so the
// metronomic control below must still fire.
func quotedMessage(n int, filler func(int) string) string {
	msg := "The file %s has uncommitted changes. Please stash them before undoing."
	var b strings.Builder
	for i := range n {
		b.WriteString(filler(i))
		fmt.Fprintf(&b, msg+"\n", "gitcmds.go")
	}
	return b.String()
}

func deliberation() func(int) string {
	return func(i int) string {
		switch i % 3 {
		case 0:
			return "Hmm, which is the long one here? Let me reconsider.\n"
		case 1:
			return "Wait.\n"
		case 2:
			return "Right. That one has a \"Please\" that can go, and the test pins only the prefix. Line 55. " +
				"The uncommitted-changes message at line 192 has the same shape. Let me check.\n"
		}
		return ""
	}
}

func metronome() func(int) string { return func(int) string { return "" } }

func TestLoopDetectorSeparatesQuotedMessagesFromLoops(t *testing.T) {
	if f := feedAll(t, loopReasoning, quotedMessage(loopMinCount+3, deliberation())); f != nil {
		t.Errorf("fired on a message quoted between rounds of deliberation: %q ×%d", f.Sample, f.Count)
	}
	// The fixture must be one the old rule fired on, or it is decorative: with
	// loopMinCount+ quotes at mean spacing well under the old loopMaxAvgGap,
	// the average-gap check this spacing rule replaced reported a loop here.
	msg := "The file gitcmds.go has uncommitted changes. Please stash them before undoing."
	text := quotedMessage(loopMinCount+3, deliberation())
	if got := strings.Count(text, msg); got < loopMinCount {
		t.Errorf("fixture quotes the message %d times, below loopMinCount; it proves nothing", got)
	}
	// The same message, metronomic: a real loop over the very same words. In
	// the answer stream, where the detector expected to earn less of its keep
	// and where the fixture's words are at home.
	if f := feedAll(t, loopAnswer, quotedMessage(loopMinCount+3, metronome())); f == nil {
		t.Error("no loop for a message repeated at its period")
	}
}

// The strip must not eat a stutter rendered one word per line. An earlier
// version of this transform in script/find-loops.py did exactly that, and it
// was caught by re-running the corpus rather than by a unit test -- so here is
// the unit test.
func TestStripKeepsAOneWordPerLineStutter(t *testing.T) {
	if f := feedAll(t, loopReasoning, strings.Repeat("Dynamical\n", 84)); f == nil {
		t.Fatal("the strip deleted a real stutter")
	}
	// A path, though, is scaffolding and goes.
	var s loopStream
	for _, line := range []string{"main.go", "internal/coder/loopdetect.go", "./x.py"} {
		if s.keepLine(line) {
			t.Errorf("kept %q, which is a path", line)
		}
	}
	for _, line := range []string{"Dynamical", "however", "Yes.", "a:b c"} {
		if !s.keepLine(line) {
			t.Errorf("dropped %q, which is prose", line)
		}
	}
}

// A fence that never closes must not blind the detector to everything after it
// -- but nothing after it exists, so what this really pins is that the
// unterminated tail is held back rather than scanned.
func TestUnclosedFenceHoldsBackItsContents(t *testing.T) {
	if f := feedAll(t, loopReasoning, "Here:\n```\n"+gridBlock()); f != nil {
		t.Errorf("fired inside an unclosed fence: %q", f.Sample)
	}
}

// Prose with a colon is not a data field. isDataField is the widest of the new
// rules and therefore the one most likely to eat a real loop.
func TestDataFieldRuleDoesNotEatProse(t *testing.T) {
	var s loopStream
	prose := []string{
		"Note: the following is wrong",
		"Then: I will read it again",
		"So the plan is: read, edit, verify",
		"I need to check the file again to be sure of the contents.",
		"note: stop.",
		"- Note: stop",
	}
	for _, line := range prose {
		if !s.keepLine(line) {
			t.Errorf("dropped prose %q", line)
		}
	}
	data := []string{`"name": "widget",`, `  "enabled": true`, "weight: 42", "stroke-width: 1px;", "- enabled: true"}
	for _, line := range data {
		if s.keepLine(line) {
			t.Errorf("kept data line %q", line)
		}
	}
	if f := feedAll(t, loopReasoning, strings.Repeat("Again: Again\n", loopMinWordRun+2)); f == nil {
		t.Error("a one-token prose loop was hidden by the data-field rule")
	}
}

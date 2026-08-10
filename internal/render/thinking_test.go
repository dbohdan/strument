package render

import (
	"bytes"
	"strings"
	"testing"
)

// drive feeds deltas to a plain Thinking and returns what it wrote.
func drive(display ThinkingDisplay, deltas ...string) string {
	var buf bytes.Buffer
	t := PlainThinking(&buf, display)
	for _, d := range deltas {
		t.Write(d)
	}
	t.End()
	return buf.String()
}

func TestThinkingShape(t *testing.T) {
	full := ThinkingDisplay{Mode: ThinkingFull}

	// The line terminates: leaving it unterminated would glue whatever comes
	// next onto the end of the thinking.
	got := drive(full, "Let me check the output.go file.")
	if want := ThinkingOpen + " Let me check the output.go file.\n"; got != want {
		t.Errorf("one line:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, ThinkingClose) {
		t.Error("one line of thinking needs no closer")
	}

	got = drive(full, "First.\n", "Second.")
	for _, want := range []string{ThinkingOpen + "\n", "First.\n", "Second.", ThinkingClose} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("block missing or out of order: %q\n%q", want, got)
		}
		got = got[i+len(want):]
	}
	if n := strings.Count(drive(full, "First.\n", "Second."), "First."); n != 1 {
		t.Errorf("the held first line was emitted %d times, want 1", n)
	}
}

// Newlines at either end are the provider's spacing, not a second line.
func TestThinkingEdgeNewlines(t *testing.T) {
	full := ThinkingDisplay{Mode: ThinkingFull}
	for _, text := range []string{"\nJust a thought.", "Just a thought.\n", "\n Just a thought. \n"} {
		if got := drive(full, text); strings.Contains(got, ThinkingClose) {
			t.Errorf("%q should stay inline, got %q", text, got)
		}
	}
}

// A cap keeps the first lines and says how many it left, in the diff
// renderer's idiom so the elision reads as native.
func TestThinkingCap(t *testing.T) {
	body := "one\ntwo\nthree\nfour\nfive\nsix"
	got := drive(ThinkingDisplay{Mode: ThinkingCapped, Lines: 3}, body)

	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(got, want) {
			t.Errorf("kept lines should include %q:\n%q", want, got)
		}
	}
	for _, gone := range []string{"four", "five", "six"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q is past the cap and should not appear:\n%q", gone, got)
		}
	}
	if !strings.Contains(got, "… 3 more lines of thinking …") {
		t.Errorf("missing the elision note:\n%q", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), ThinkingClose) {
		t.Errorf("a capped block still closes:\n%q", got)
	}
}

// The cap counts lines, so a one-line block is untouched by any cap at all —
// which is most blocks.
func TestThinkingCapLeavesOneLinerAlone(t *testing.T) {
	for _, n := range []int{1, 3, 10} {
		got := drive(ThinkingDisplay{Mode: ThinkingCapped, Lines: n}, "Just a thought.")
		if want := ThinkingOpen + " Just a thought.\n"; got != want {
			t.Errorf("cap %d:\n got %q\nwant %q", n, got, want)
		}
	}
}

// Singular, because "1 more lines" is the kind of thing that gets noticed.
func TestThinkingCapSingular(t *testing.T) {
	got := drive(ThinkingDisplay{Mode: ThinkingCapped, Lines: 1}, "one\ntwo")
	if !strings.Contains(got, "… 1 more line of thinking …") {
		t.Errorf("want a singular elision note:\n%q", got)
	}
}

// Off renders nothing at all — not even a marker, so a reader is not told
// there was something they are not seeing.
func TestThinkingOff(t *testing.T) {
	got := drive(ThinkingDisplay{Mode: ThinkingOff}, "one\ntwo\nthree")
	if got != "" {
		t.Errorf("off wrote %q", got)
	}
}

// End reports whether a block was rendered, which is what the callers use to
// decide whether a separator is owed.
func TestThinkingEndReportsWhetherItRendered(t *testing.T) {
	for _, tc := range []struct {
		name    string
		display ThinkingDisplay
		delta   string
		want    bool
	}{
		{"text", ThinkingDisplay{Mode: ThinkingFull}, "A thought.", true},
		{"empty", ThinkingDisplay{Mode: ThinkingFull}, "", false},
		{"whitespace", ThinkingDisplay{Mode: ThinkingFull}, " \n ", false},
		{"off", ThinkingDisplay{Mode: ThinkingOff}, "A thought.", false},
	} {
		var buf bytes.Buffer
		th := PlainThinking(&buf, tc.display)
		th.Write(tc.delta)
		if got := th.End(); got != tc.want {
			t.Errorf("%s: End = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Deltas split mid-line must not change the shape: the first line is held
// until a newline settles it, however many pieces it arrives in.
func TestThinkingIndifferentToDeltaBoundaries(t *testing.T) {
	full := ThinkingDisplay{Mode: ThinkingFull}
	whole := drive(full, "First line.\nSecond line.")
	split := drive(full, "First ", "line.", "\n", "Second ", "line.")
	if whole != split {
		t.Errorf("split deltas rendered differently:\n whole %q\n split %q", whole, split)
	}
}

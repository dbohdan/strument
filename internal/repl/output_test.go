package repl

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/render"
)

// TestThinkingIsBracketed confirms the thinking block is opened by a labelled
// header and closed by a bare rule, with the answer plain after it.
//
// aider labels the answer too. That was true of a turn that was one reply;
// in a loop the label lands on every step and mostly precedes tool calls
// rather than an answer, so only the block that needs marking off gets a name.
func TestThinkingIsBracketed(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamReasoning("weighing options")
	o.StreamText("here is the answer")
	o.FlushStream()
	got := buf.String()

	rule := strings.Repeat("-", 40)
	for _, want := range []string{"► THINKING", "weighing options", "here is the answer", rule} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "ANSWER") {
		t.Errorf("the answer is no longer labelled:\n%q", got)
	}
	// header, thinking, closing rule, answer — in that order.
	for _, want := range []string{"► THINKING", "weighing options", rule, "here is the answer"} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("out of order at %q:\n%q", want, got)
		}
		got = got[i+len(want):]
	}
	if n := strings.Count(buf.String(), rule); n != 2 {
		t.Errorf("the block should be bracketed by two rules, found %d:\n%q", n, buf.String())
	}
}

// TestThinkingIsDimmed: the whole block renders in the recessive color, so it
// reads as an aside rather than competing with the answer.
func TestThinkingIsDimmed(t *testing.T) {
	var buf bytes.Buffer
	theme := render.DefaultTheme()
	o := &termOutput{w: &buf, color: true, theme: theme, width: 40}
	o.StreamReasoning("weighing options")
	o.StreamText("here is the answer")
	o.FlushStream()
	got := buf.String()

	think, answer, ok := strings.Cut(got, "weighing options")
	if !ok {
		t.Fatalf("reasoning missing:\n%q", got)
	}
	if !strings.Contains(think, "\x1b["+theme.Reasoning+"m") {
		t.Errorf("thinking is not in the recessive color:\n%q", think)
	}
	if !strings.Contains(answer, "\x1b["+theme.Assistant+"m") {
		t.Errorf("the answer should return to the assistant color:\n%q", answer)
	}
}

// TestWaitingClearedOnFirstOutput confirms the "Waiting for <model>" line is
// shown and then erased (CR + clear-line) before the first streamed byte.
func TestWaitingClearedOnFirstOutput(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.startWaiting("Test Model")
	o.StreamText("hi")
	o.FlushStream()
	got := buf.String()

	if !strings.Contains(got, "Waiting for Test Model") {
		t.Errorf("waiting message missing:\n%q", got)
	}
	erase := strings.Index(got, "\r\x1b[K")
	if erase < 0 {
		t.Fatalf("waiting line not erased (no CR + clear-line):\n%q", got)
	}
	if erase >= strings.Index(got, "hi") {
		t.Errorf("erase must precede the answer text:\n%q", got)
	}
}

// TestAnswerOnlyNoHeaders confirms a response with no reasoning shows neither
// header — aider only frames reasoning.
func TestAnswerOnlyNoHeaders(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamText("just the answer")
	o.FlushStream()
	got := buf.String()

	if strings.Contains(got, "THINKING") || strings.Contains(got, "ANSWER") {
		t.Errorf("a no-reasoning answer must not show the headers:\n%q", got)
	}
	if !strings.Contains(got, "just the answer") {
		t.Errorf("answer text missing:\n%q", got)
	}
}

// editArgs builds a complete edit call's JSON arguments.
func editArgs(path, old, replacement string) string {
	return fmt.Sprintf(`{"path":%q,"old_string":%q,"new_string":%q}`, path, old, replacement)
}

// TestWhitespaceBetweenEditsAddsNoBlankLines pins a bug found in a real
// terminal: a model that emits a lone newline between tool calls got one blank
// line per edit, stacked under the first header.
//
// The mechanism is worth knowing, because it is not obvious from either side.
// An edit's body is buffered until Flush, but a content delta goes straight to
// the terminal — and merely creating the markdown renderer for it is what makes
// the next tool call emit its separator. So the blank lines appeared where the
// text was, above every diff, rather than where the model had put them.
func TestWhitespaceBetweenEditsAddsNoBlankLines(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 60}
	o.StreamText("Fixing three things.")
	o.StreamToolCall(0, "edit", editArgs("a.md", "alpha", "ALPHA"))
	o.StreamText("\n") // a habit of line breaks, not a request for one
	o.StreamToolCall(1, "edit", editArgs("a.md", "beta", "BETA"))
	o.StreamText("") // some providers send empty content deltas too
	o.StreamToolCall(2, "edit", editArgs("a.md", "gamma", "GAMMA"))
	o.FlushStream()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] == "" && lines[i-1] == "" {
			t.Errorf("consecutive blank lines at %d:\n%q", i, buf.String())
		}
	}
	if n := strings.Count(buf.String(), "a.md"); n != 1 {
		t.Errorf("the file is named %d times, want once:\n%s", n, buf.String())
	}
}

// TestWhitespaceOnlyAnswerRendersNothing: a send whose entire content is
// whitespace must not push an ANSWER header and a blank line ahead of the
// diffs. Seen with providers that pad tool-call turns with a newline.
func TestWhitespaceOnlyAnswerRendersNothing(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 60}
	o.StreamText("\n")
	o.StreamText("  ")
	o.StreamToolCall(0, "edit", editArgs("a.md", "alpha", "ALPHA"))
	o.FlushStream()

	if got := buf.String(); !strings.HasPrefix(got, "a.md\n") {
		t.Errorf("output should open with the diff header, got:\n%q", got)
	}
}

// TestEachFileNamedOnceInARun: consecutive edits to one file print its name
// once and are separated by a blank line; a new file names itself.
func TestEachFileNamedOnceInARun(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 60}
	o.StreamToolCall(0, "edit", editArgs("a.md", "alpha", "ALPHA"))
	o.StreamToolCall(1, "edit", editArgs("a.md", "beta", "BETA"))
	o.StreamToolCall(2, "edit", editArgs("b.md", "gamma", "GAMMA"))
	o.StreamToolCall(3, "edit", editArgs("b.md", "delta", "DELTA"))
	o.FlushStream()

	got := buf.String()
	for _, f := range []string{"a.md", "b.md"} {
		if n := strings.Count(got, f); n != 1 {
			t.Errorf("%s named %d times, want once:\n%s", f, n, got)
		}
	}
	// Order still has to hold: each file's own edits stay under its name.
	for _, want := range []string{"a.md", "- alpha", "- beta", "b.md", "- gamma", "- delta"} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("missing %q:\n%s", want, got)
		}
		got = got[i:]
	}
}

// TestObservationOnlySendLeavesNoBlankLine: read, grep, glob, ls, and verify
// draw nothing in the stream — they print their own one-line outcome when they
// run — so a send made only of those must not leave a separator behind it. It
// did, and the blank landed above the outcome line, where it read as a gap in
// the transcript rather than as spacing.
func TestObservationOnlySendLeavesNoBlankLine(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 60}
	o.StreamToolCall(0, "read", `{"path":"notes.md"}`)
	o.StreamToolCall(1, "grep", `{"pattern":"Sonnerie"}`)
	o.FlushStream()

	if got := buf.String(); got != "" {
		t.Errorf("a send that drew nothing wrote %q", got)
	}
}

// TestProseBetweenCallsStaysBetweenThem: an edit's body is not written until
// the flush, so prose rendered the moment it arrived appeared *above* the diff
// it was written after — a caption attached to the wrong picture. It now
// travels with the tool calls and keeps the model's order.
func TestProseBetweenCallsStaysBetweenThem(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 60}
	o.StreamText("First the heading.")
	o.StreamToolCall(0, "edit", editArgs("a.md", "alpha", "ALPHA"))
	o.StreamText("Now the body.")
	o.StreamToolCall(1, "edit", editArgs("a.md", "beta", "BETA"))
	o.FlushStream()

	got := buf.String()
	for _, want := range []string{
		"First the heading.",
		"- alpha", "+ ALPHA",
		"Now the body.",
		"- beta", "+ BETA",
	} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("missing or out of order: %q\n%s", want, buf.String())
		}
		got = got[i+len(want):]
	}
	// Prose ends the run, so the file is named again after it rather than
	// separated by a blank line that would now read as "same file as above".
	if n := strings.Count(buf.String(), "a.md"); n != 2 {
		t.Errorf("a.md named %d times, want 2 (once per run):\n%s", n, buf.String())
	}
}

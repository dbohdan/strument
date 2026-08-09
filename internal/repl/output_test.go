package repl

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/render"
)

// TestReasoningHeaders confirms reasoning and answer stream through one
// markdown renderer, separated by the THINKING and ANSWER headers (a
// full-width rule + a bold ► label), mirroring aider.
func TestReasoningHeaders(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamReasoning("weighing options")
	o.StreamText("here is the answer")
	o.FlushStream()
	got := buf.String()

	rule := strings.Repeat("-", 40)
	for _, want := range []string{"► THINKING", "► ANSWER", "weighing options", "here is the answer", rule} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%q", want, got)
		}
	}
	if i, j := strings.Index(got, "► THINKING"), strings.Index(got, "► ANSWER"); i < 0 || j < 0 || i >= j {
		t.Errorf("THINKING must precede ANSWER (%d vs %d):\n%q", i, j, got)
	}
	if strings.Index(got, "weighing options") >= strings.Index(got, "► ANSWER") {
		t.Errorf("reasoning must appear before the ANSWER header:\n%q", got)
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

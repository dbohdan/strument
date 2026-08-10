package repl

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/render"
)

// TestThinkingIsInlineWhenItIsOneLine pins the common shape. In a seven-step
// session, five of the seven thinking blocks were a single sentence restating
// the tool call that followed — a banner cannot be a prefix, which is why the
// banner had to go rather than merely shrink.
func TestThinkingIsInlineWhenItIsOneLine(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamReasoning("Let me check the output.go file.")
	o.StreamText("Here is the answer.")
	o.FlushStream()

	got := buf.String()
	want := render.ThinkingOpen + " Let me check the output.go file."
	if !strings.HasPrefix(got, want) {
		t.Errorf("thinking should open inline with %q:\n%q", want, got)
	}
	if strings.Contains(got, render.ThinkingClose) {
		t.Errorf("one line of thinking needs no closer:\n%q", got)
	}
	if strings.Contains(got, "THINKING") || strings.Contains(got, strings.Repeat("-", 40)) {
		t.Errorf("the banner and its rules should be gone:\n%q", got)
	}
}

// TestThinkingIsBracketedWhenItRunsOn: past one line, the marker takes a line of
// its own and the block is closed, so a long trace has a findable end.
func TestThinkingIsBracketedWhenItRunsOn(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
	o.StreamReasoning("I can see the header function.\n")
	o.StreamReasoning("It uses the assistant color.")
	o.StreamText("Here is the answer.")
	o.FlushStream()

	got := buf.String()
	if !strings.HasPrefix(got, render.ThinkingOpen+"\n") {
		t.Errorf("a block should open on its own line:\n%q", got)
	}
	for _, want := range []string{
		render.ThinkingOpen, "I can see the header function.", "It uses the assistant color.",
		render.ThinkingClose, "Here is the answer.",
	} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("missing or out of order: %q\n%s", want, buf.String())
		}
		got = got[i+len(want):]
	}
	if n := strings.Count(buf.String(), "I can see the header function."); n != 1 {
		t.Errorf("the held first line was emitted %d times, want 1:\n%s", n, buf.String())
	}
}

// A newline before or after the thinking is the provider's spacing, not a
// second line — only an interior one makes a block.
func TestThinkingEdgeNewlinesDoNotMakeABlock(t *testing.T) {
	for _, text := range []string{"\nJust a thought.", "Just a thought.\n", "\n Just a thought. \n"} {
		var buf bytes.Buffer
		o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
		o.StreamReasoning(text)
		o.FlushStream()
		if got := buf.String(); strings.Contains(got, render.ThinkingClose) {
			t.Errorf("%q should stay inline:\n%q", text, got)
		}
	}
}

// TestThinkingLeavesOneBlankLine: thinking sets the streamed flag, so a tool
// call that draws nothing of its own — a read, whose outcome prints later
// through Toolf — used to collect a second blank line under the separator.
func TestThinkingLeavesOneBlankLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drive func(*termOutput)
	}{
		{"then an observation call", func(o *termOutput) {
			o.StreamReasoning("A thought.")
			o.StreamToolCall(0, "read", `{"path":"a.md"}`)
		}},
		{"then an edit", func(o *termOutput) {
			o.StreamReasoning("A thought.")
			o.StreamToolCall(0, "edit", editArgs("a.md", "alpha", "ALPHA"))
		}},
		{"then an answer", func(o *termOutput) {
			o.StreamReasoning("A thought.")
			o.StreamText("The answer.")
		}},
		{"alone", func(o *termOutput) { o.StreamReasoning("A thought.") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
			tc.drive(o)
			o.FlushStream()
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			for i := 1; i < len(lines); i++ {
				if lines[i] == "" && lines[i-1] == "" {
					t.Errorf("consecutive blank lines at %d:\n%q", i, buf.String())
				}
			}
		})
	}
}

// TestThinkingIsFaint: the whole block, marker included, renders faint — which
// is a modifier rather than a color, so it dims whatever palette the user has
// instead of betting on one. Theme.Reasoning explains why at length.
func TestThinkingIsFaint(t *testing.T) {
	var buf bytes.Buffer
	theme := render.DefaultTheme()
	o := &termOutput{w: &buf, color: true, theme: theme, width: 40}
	o.StreamReasoning("Weighing options.")
	o.StreamText("Here is the answer.")
	o.FlushStream()

	got := buf.String()
	think, answer, ok := strings.Cut(got, "Weighing options")
	if !ok {
		t.Fatalf("reasoning missing:\n%q", got)
	}
	if !strings.Contains(think, "\x1b["+theme.Reasoning+"m") {
		t.Errorf("thinking is not faint:\n%q", think)
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

// TestEmptyReasoningRendersNothing: a provider padding a turn with an empty
// reasoning delta has streamed nothing, and must not buy a blank line with no
// content above it — the same shape of wart that whitespace content deltas had.
func TestEmptyReasoningRendersNothing(t *testing.T) {
	for _, pad := range []string{"", "\n", "   \n  "} {
		var buf bytes.Buffer
		o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 40}
		o.StreamReasoning(pad)
		o.StreamToolCall(0, "read", `{"path":"a.md"}`)
		o.FlushStream()
		if got := buf.String(); got != "" {
			t.Errorf("reasoning of %q wrote %q", pad, got)
		}
	}
}

// TestNoDoubledBlankLines drives the sequences that produced them, each of
// which pairs two separators that were individually correct: an answer and a
// tool call that draws nothing, thinking and the same, and the trailing newline
// meeting the usage line's leading one.
func TestNoDoubledBlankLines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drive func(*termOutput)
	}{
		{"answer then an observation call", func(o *termOutput) {
			o.StreamText("Let me search the codebase.")
			o.StreamToolCall(0, "grep", `{"pattern":"maxSteps"}`)
		}},
		{"thinking, answer, observation call", func(o *termOutput) {
			o.StreamReasoning("Considering.")
			o.StreamText("Let me look.")
			o.StreamToolCall(0, "read", `{"path":"a.md"}`)
		}},
		{"answer then the usage line", func(o *termOutput) {
			o.StreamText("Here is the summary.")
			o.FlushStream()
			o.Printf("") // the usage line opens with a blank, as aider's did
			o.Printf("Tokens: 1.0k sent, 20 received.")
		}},
		{"thinking block then the usage line", func(o *termOutput) {
			o.StreamReasoning("One.\nTwo.")
			o.FlushStream()
			o.Printf("")
			o.Printf("Tokens: 1.0k sent, 20 received.")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme(), width: 60}
			tc.drive(o)
			o.FlushStream()
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			for i := 1; i < len(lines); i++ {
				if lines[i] == "" && lines[i-1] == "" {
					t.Errorf("consecutive blank lines at %d:\n%q", i, buf.String())
				}
			}
		})
	}
}

// The guard only ever drops a bare newline, so content that happens to contain
// blank lines is untouched.
func TestBlankGuardKeepsContent(t *testing.T) {
	var buf bytes.Buffer
	g := &blankGuard{w: &buf}
	for _, chunk := range []string{"a\n", "\n", "\n", "\n\nb\n", "\n", "\n"} {
		if _, err := g.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := buf.String(), "a\n\n\n\nb\n\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestBothOutputsAgreeOnShape is the point of putting the thinking renderer in
// render: the terminal and a redirected run must lay a block out the same way,
// differing only in the color the terminal adds. They disagreed completely
// before — script mode rendered nothing at all.
func TestBothOutputsAgreeOnShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		display render.ThinkingDisplay
		deltas  []string
	}{
		{"one line", render.ThinkingDisplay{}, []string{"Let me check output.go."}},
		{"a block", render.ThinkingDisplay{}, []string{"First.\n", "Second.\nThird."}},
		{"capped", render.ThinkingDisplay{Mode: render.ThinkingCapped, Lines: 2},
			[]string{"one\ntwo\nthree\nfour"}},
		{"off", render.ThinkingDisplay{Mode: render.ThinkingOff}, []string{"one\ntwo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var term bytes.Buffer
			o := &termOutput{w: &term, color: false, theme: render.DefaultTheme(), width: 200}
			o.Thinking = tc.display
			for _, d := range tc.deltas {
				o.StreamReasoning(d)
			}
			o.endReasoning()

			var plain bytes.Buffer
			p := render.PlainThinking(&plain, tc.display)
			for _, d := range tc.deltas {
				p.Write(d)
			}
			p.End()

			got := strings.TrimRight(term.String(), "\n")
			want := strings.TrimRight(plain.String(), "\n")
			if got != want {
				t.Errorf("the two outputs disagree:\n terminal %q\n plain    %q", got, want)
			}
		})
	}
}

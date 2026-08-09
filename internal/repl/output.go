// Package repl is the interactive layer: readline
// input with slash commands, double-Ctrl-C chords, and live-rendered
// markdown streaming via internal/render.
package repl

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"dbohdan.com/strument/internal/render"
)

// streamPhase tracks whether the current turn is streaming reasoning, the
// answer, or neither yet — so the THINKING/ANSWER headers land once each.
type streamPhase int

const (
	phaseNone streamPhase = iota
	phaseReasoning
	phaseAnswer
)

// termOutput implements coder.Output for a terminal: reasoning and answer
// deltas both stream through the markdown renderer as they arrive, separated
// by the THINKING/ANSWER headers, like aider.
type termOutput struct {
	w     io.Writer
	color bool
	theme render.Theme
	width int // terminal width, for the markdown renderer's full-width rules

	parser       *render.Parser
	diffs        *render.ToolDiffSet
	phase        streamPhase
	streamed     bool
	cursorHidden bool
	waiting      bool

	// answerVisible records that this send's answer has said something, and
	// toolStarted that a tool call has begun streaming. Between them they decide
	// when a whitespace-only content delta is worth rendering; see StreamText.
	answerVisible bool
	toolStarted   bool

	// held renders prose that arrives after a tool call has begun. It cannot go
	// straight to the terminal: an edit's body is not written until the flush,
	// so text rendered immediately would appear above the diff it was written
	// after. Buffered here, it rejoins the tool calls in the model's own order.
	held    *render.Parser
	heldBuf bytes.Buffer
}

// startWaiting shows a "Waiting for <model> " line (no newline) while the
// request is in flight — aider's cue so a slow-to-wake model doesn't look
// hung. clearWaiting erases it before the first output.
func (o *termOutput) startWaiting(name string) {
	o.waiting = true
	fmt.Fprintf(o.w, "Waiting for %s", name)
}

// clearWaiting erases the waiting line if one is showing, returning the cursor
// to the start of the (now blank) line so streamed output starts cleanly.
func (o *termOutput) clearWaiting() {
	if o.waiting {
		o.waiting = false
		fmt.Fprint(o.w, "\r\x1b[K")
	}
}

// hideCursor blanks the terminal cursor for the duration of a streaming
// render — aider's touch: the blinking caret chasing the text is
// distracting. Emitted once per stream (gated on color, like the styling).
func (o *termOutput) hideCursor() {
	if o.color && !o.cursorHidden {
		fmt.Fprint(o.w, "\x1b[?25l")
		o.cursorHidden = true
	}
}

// showCursor restores the cursor. Idempotent, so FlushStream and the
// runTurn safety-net defer can both call it.
func (o *termOutput) showCursor() {
	if o.cursorHidden {
		fmt.Fprint(o.w, "\x1b[?25h")
		o.cursorHidden = false
	}
}

func (o *termOutput) sgr(codes string) string {
	if !o.color || codes == "" {
		return ""
	}
	return "\x1b[" + codes + "m"
}

func (o *termOutput) Printf(format string, args ...any) {
	o.clearWaiting()
	fmt.Fprintf(o.w, format+"\n", args...)
}

func (o *termOutput) Toolf(format string, args ...any) {
	o.clearWaiting()
	fmt.Fprintf(o.w, o.sgr(o.theme.Tool)+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) Warningf(format string, args ...any) {
	o.clearWaiting()
	fmt.Fprintf(o.w, o.sgr(o.theme.Warning)+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) Errorf(format string, args ...any) {
	o.clearWaiting()
	fmt.Fprintf(o.w, o.sgr(o.theme.Error)+format+o.sgr("0")+"\n", args...)
}

// ensureParser opens the markdown renderer on first use in a turn.
func (o *termOutput) ensureParser() {
	if o.parser == nil {
		o.parser = render.NewParser(render.NewANSI(o.w, o.color, o.theme, o.width))
	}
}

// closeParser flushes and closes the markdown renderer, resetting its color
// and leaving the cursor on a fresh line so what follows starts cleanly.
func (o *termOutput) closeParser() {
	if o.parser == nil {
		return
	}
	o.parser.End()
	fmt.Fprint(o.w, o.sgr("0")) // the renderer keeps the base color open
	if !o.parser.AtLineStart() {
		fmt.Fprintln(o.w)
	}
	o.parser = nil
}

// header renders a labelled separator directly (not through the markdown
// renderer, so the spacing is exact): a blank line, a full-width dashed rule, a
// blank line, a bold "► LABEL", and a blank line — aider's shape.
//
// Only THINKING uses it now. aider's matching "► ANSWER" said something true of
// a turn that was one reply: everything after it was the answer. In a loop it
// labels the wrong thing — most of what follows is tool calls, and the label
// lands again on every step. Thinking is what needs marking off; the answer is
// just everything else, and rule() closes the block instead.
func (o *termOutput) header(label string) {
	width := o.width
	if width <= 0 {
		width = 80
	}
	fmt.Fprintf(o.w, "\n%s%s%s\n\n%s► %s%s\n\n",
		o.sgr(o.theme.Assistant), strings.Repeat("-", width), o.sgr("0"),
		o.sgr(o.reasoningTheme().Assistant)+o.sgr("1"), label, o.sgr("0"))
}

// rule closes the thinking block: the same separator without a label. It is
// what tells a reader the dimmed text has ended, and it is not decoration —
// without color there would be nothing else to say so.
func (o *termOutput) rule() {
	width := o.width
	if width <= 0 {
		width = 80
	}
	fmt.Fprintf(o.w, "\n%s%s%s\n\n",
		o.sgr(o.theme.Assistant), strings.Repeat("-", width), o.sgr("0"))
}

// reasoningTheme is the palette the thinking block renders in: the ordinary one
// with its body color made recessive, so the whole block reads as an aside
// rather than competing with the answer.
func (o *termOutput) reasoningTheme() render.Theme {
	t := o.theme
	if t.Reasoning != "" {
		t.Assistant = t.Reasoning
		t.Code = t.Reasoning // a code span inside thinking is still thinking
	}
	return t
}

func (o *termOutput) StreamReasoning(delta string) {
	o.clearWaiting()
	o.hideCursor()
	if o.phase != phaseReasoning {
		o.header("THINKING") // cursor is at column 0 after clearWaiting
		o.phase = phaseReasoning
	}
	if o.parser == nil {
		o.parser = render.NewParser(render.NewANSI(o.w, o.color, o.reasoningTheme(), o.width))
	}
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamText(delta string) {
	// Providers interleave content deltas with tool calls, and some of those
	// deltas say nothing — an empty string, or a lone newline between two edit
	// calls. Rendering them is not free: creating the parser is what makes the
	// *next* tool call emit its separator, so a model in the habit of putting a
	// newline between calls bought one blank line per edit.
	//
	// Whitespace is dropped in two situations, for the same reason in both:
	// there is nothing for it to space apart. Before the answer has said
	// anything, it would only push the ANSWER header down. After a tool call has
	// begun, the diffs space themselves, and the model's own line breaks are a
	// habit of composing prose rather than a request for a blank line here.
	if strings.TrimSpace(delta) == "" && (!o.answerVisible || o.toolStarted) {
		return
	}
	o.answerVisible = true

	// Once a call has begun, prose belongs in the sequence with the diffs, not
	// ahead of them.
	if o.toolStarted {
		if o.held == nil {
			o.held = render.NewParser(render.NewANSI(&o.heldBuf, o.color, o.theme, o.width))
		}
		o.held.Write(delta)
		return
	}

	o.clearWaiting()
	o.hideCursor()
	if o.phase == phaseReasoning {
		o.closeParser() // end THINKING, land on a fresh line
		o.rule()
	}
	o.phase = phaseAnswer
	o.ensureParser()
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamToolCall(index int, name, args string) {
	o.clearWaiting()
	o.hideCursor()
	// A tool-call turn ends the markdown answer (finish_reason: tool_calls),
	// so close the parser before the diff begins; they never interleave.
	if o.phase == phaseReasoning {
		o.closeParser()
		o.rule() // reasoning gave way to edits: close the block
		o.phase = phaseAnswer
	}
	if o.parser != nil {
		o.closeParser()
		fmt.Fprintln(o.w) // separate the answer text from the first diff header
	}
	if o.diffs == nil {
		o.diffs = render.NewToolDiffSet(o.w, o.color, o.theme)
	}
	o.flushHeld() // whatever was said since the last call goes in before this one
	o.toolStarted = true
	// streamed is set at the flush, from whether the set actually drew: a send
	// of nothing but read and grep calls writes nothing here, and marking it as
	// streamed left a blank line above their outcome lines.
	o.diffs.Write(index, name, args)
}

// flushHeld closes the held renderer and hands its bytes to the diff set, so
// they take their place in the sequence rather than racing the diffs to the
// terminal.
func (o *termOutput) flushHeld() {
	if o.held == nil {
		return
	}
	o.held.End()
	if o.color {
		fmt.Fprint(&o.heldBuf, "\x1b[0m") // the renderer leaves its base color open
	}
	o.held = nil
	if o.diffs != nil {
		o.diffs.Text(o.heldBuf.Bytes())
	}
	o.heldBuf.Reset()
}

func (o *termOutput) FlushStream() {
	o.clearWaiting()
	o.closeParser()
	o.flushHeld()
	if o.diffs != nil {
		if o.diffs.Drew() {
			o.streamed = true
		}
		o.diffs.Flush()
		o.diffs = nil
	}
	if o.streamed {
		fmt.Fprintln(o.w)
		o.streamed = false
	}
	o.phase = phaseNone
	o.answerVisible = false
	o.toolStarted = false
	o.showCursor()
}

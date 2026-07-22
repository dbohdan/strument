// Package repl is the interactive layer: readline
// input with slash commands, double-Ctrl-C chords, and live-rendered
// markdown streaming via internal/render.
package repl

import (
	"fmt"
	"io"

	"dbohdan.com/strument/internal/render"
)

// termOutput implements coder.Output for a terminal: assistant answer
// deltas stream through the markdown renderer as they arrive; reasoning
// deltas print dim and unparsed (display-only).
type termOutput struct {
	w     io.Writer
	color bool
	theme render.Theme
	width int // terminal width, for the markdown renderer's full-width rules

	parser       *render.Parser
	diffs        *render.ToolDiffSet
	inReasoning  bool
	streamed     bool
	cursorHidden bool
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
	fmt.Fprintf(o.w, format+"\n", args...)
}

func (o *termOutput) Warningf(format string, args ...any) {
	fmt.Fprintf(o.w, o.sgr(o.theme.Warning)+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) Errorf(format string, args ...any) {
	fmt.Fprintf(o.w, o.sgr(o.theme.Error)+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) StreamText(delta string) {
	o.hideCursor()
	o.endReasoning()
	if o.parser == nil {
		o.parser = render.NewParser(render.NewANSI(o.w, o.color, o.theme, o.width))
	}
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamToolCall(index int, name, args string) {
	o.hideCursor()
	o.endReasoning()
	// A tool-call turn ends the markdown answer (finish_reason: tool_calls),
	// so close the parser before the diff begins; they never interleave.
	if o.parser != nil {
		o.parser.End()
		// The markdown renderer leaves the cursor mid-line after a paragraph,
		// so break to a fresh line (after resetting the answer color) before
		// the first diff header — otherwise it glues onto the answer text.
		fmt.Fprint(o.w, o.sgr("0"))
		fmt.Fprintln(o.w)
		o.parser = nil
	}
	if o.diffs == nil {
		o.diffs = render.NewToolDiffSet(o.w, o.color, o.theme)
	}
	o.streamed = true
	o.diffs.Write(index, name, args)
}

func (o *termOutput) StreamReasoning(delta string) {
	o.hideCursor()
	if !o.inReasoning {
		o.inReasoning = true
		fmt.Fprint(o.w, o.sgr(o.theme.Assistant)+"\n► THINKING\n\n")
	}
	o.streamed = true
	fmt.Fprint(o.w, delta)
}

// endReasoning closes the dim reasoning section when the answer starts.
func (o *termOutput) endReasoning() {
	if !o.inReasoning {
		return
	}
	o.inReasoning = false
	fmt.Fprint(o.w, o.sgr("0")+"\n\n")
}

func (o *termOutput) FlushStream() {
	o.endReasoning()
	if o.parser != nil {
		o.parser.End()
		// The renderer keeps the assistant base color open on every line;
		// reset once here so it does not bleed into the next prompt.
		fmt.Fprint(o.w, o.sgr("0"))
		o.parser = nil
	}
	if o.diffs != nil {
		o.diffs.Flush()
		o.diffs = nil
	}
	if o.streamed {
		fmt.Fprintln(o.w)
		o.streamed = false
	}
	o.showCursor()
}

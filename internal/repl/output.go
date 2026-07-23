// Package repl is the interactive layer: readline
// input with slash commands, double-Ctrl-C chords, and live-rendered
// markdown streaming via internal/render.
package repl

import (
	"fmt"
	"io"

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

// The reasoning headers, mirroring aider's reasoning_tags.py: markdown fed
// into the same renderer as the answer, so the "---" becomes a full-width
// rule and THINKING/ANSWER render bold. The ANSWER header leads with a blank
// line so the rule is a thematic break, not a setext underline of the last
// reasoning line.
const (
	thinkingHeader = "---\n► **THINKING**\n\n"
	answerHeader   = "\n\n---\n► **ANSWER**\n\n"
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

// ensureParser opens the markdown renderer on first use in a turn.
func (o *termOutput) ensureParser() {
	if o.parser == nil {
		o.parser = render.NewParser(render.NewANSI(o.w, o.color, o.theme, o.width))
	}
}

func (o *termOutput) StreamReasoning(delta string) {
	o.hideCursor()
	o.ensureParser()
	if o.phase != phaseReasoning {
		o.parser.Write(thinkingHeader)
		o.phase = phaseReasoning
	}
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamText(delta string) {
	o.hideCursor()
	o.ensureParser()
	if o.phase == phaseReasoning {
		o.parser.Write(answerHeader) // close THINKING, open ANSWER
	}
	o.phase = phaseAnswer
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamToolCall(index int, name, args string) {
	o.hideCursor()
	// A tool-call turn ends the markdown answer (finish_reason: tool_calls),
	// so close the parser before the diff begins; they never interleave.
	if o.parser != nil {
		if o.phase == phaseReasoning {
			o.parser.Write(answerHeader) // reasoning gave way to edits: mark it
			o.phase = phaseAnswer
		}
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

func (o *termOutput) FlushStream() {
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
	o.phase = phaseNone
	o.showCursor()
}

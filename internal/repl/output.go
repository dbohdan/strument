// Package repl is the interactive layer (basecoder-spec §1.2): readline
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
// deltas print dim and unparsed (display-only, §4).
type termOutput struct {
	w     io.Writer
	color bool

	parser      *render.Parser
	inReasoning bool
	streamed    bool
}

func (o *termOutput) sgr(codes string) string {
	if !o.color {
		return ""
	}
	return "\x1b[" + codes + "m"
}

func (o *termOutput) Printf(format string, args ...any) {
	fmt.Fprintf(o.w, format+"\n", args...)
}

func (o *termOutput) Warningf(format string, args ...any) {
	fmt.Fprintf(o.w, o.sgr("33")+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) Errorf(format string, args ...any) {
	fmt.Fprintf(o.w, o.sgr("31")+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) StreamText(delta string) {
	o.endReasoning()
	if o.parser == nil {
		o.parser = render.NewParser(render.NewANSI(o.w, o.color))
	}
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamReasoning(delta string) {
	if !o.inReasoning {
		o.inReasoning = true
		fmt.Fprint(o.w, o.sgr("2")+"· thinking ·\n")
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
		o.parser = nil
	}
	if o.streamed {
		fmt.Fprintln(o.w)
		o.streamed = false
	}
}

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
	guarded bool // w has been wrapped in a blankGuard

	held    *render.Parser
	heldBuf bytes.Buffer

	// Thinking is how much of the model's reasoning to show, from the config.
	// The zero value shows all of it.
	Thinking render.ThinkingDisplay

	// think renders the reasoning block in flight; nil between blocks.
	think *render.Thinking

	// sep is the blank line between steps, owed by whatever drew last and paid
	// by the next block of thinking. See render.GroupSep.
	sep render.GroupSep
}

// blankGuard collapses a run of blank lines to one.
//
// Several places emit a separator without knowing what came before it: the
// newline between an answer and the first diff, the one FlushStream puts after
// anything the stream drew, and the blank the usage line opens with. Each is
// right on its own, and each has at some point doubled up with another — the
// same bug three times, in three different pairs.
//
// Rather than teach every separator what the others did, a lone newline is
// dropped when the output already sits on a blank line. Only a bare "\n" is
// ever suppressed, so nothing the markdown renderer or a diff writes can be
// swallowed: those always carry text with them.
type blankGuard struct {
	w   io.Writer
	run int // trailing newlines written
}

func (g *blankGuard) Write(p []byte) (int, error) {
	if len(p) == 1 && p[0] == '\n' && g.run >= 2 {
		return 1, nil // already on a blank line; claim the write and drop it
	}
	n, err := g.w.Write(p)
	g.count(p[:n])
	return n, err
}

// count advances the trailing-newline run, stepping over CSI sequences.
//
// A color change prints nothing, so it must neither extend a run nor break
// one. Counting the escape bytes as content is what made the guard miss a
// double blank whenever color was on, and only then — every test here rendered
// plain, so the whole class was invisible. Two places produce it: the
// renderer's reset, written between the last newline of a block and the
// separator that follows, and the reset the thinking's closing marker trails
// after its own newline.
//
// A sequence split across two writes is not handled; the stray ESC is treated
// as content, which costs a suppression rather than making a wrong one. Nothing
// here writes a partial sequence.
func (g *blankGuard) count(p []byte) {
	for i := 0; i < len(p); {
		if n := csiLen(p[i:]); n > 0 {
			i += n
			continue
		}
		if p[i] == '\n' {
			g.run++
		} else {
			g.run = 0
		}
		i++
	}
}

// csiLen is the length of the CSI sequence at the start of p, or 0 if there
// isn't a complete one there.
func csiLen(p []byte) int {
	if len(p) < 2 || p[0] != 0x1b || p[1] != '[' {
		return 0
	}
	i := 2
	for i < len(p) && p[i] >= 0x20 && p[i] <= 0x3f { // parameters
		i++
	}
	if i >= len(p) || p[i] < 0x40 || p[i] > 0x7e { // the final byte
		return 0
	}
	return i + 1
}

// guard wraps the writer in place, once. It is done here rather than in a
// constructor because termOutput is built as a struct literal, in repl.go and
// in every test.
func (o *termOutput) guard() {
	if !o.guarded {
		o.guarded = true
		o.w = &blankGuard{w: o.w}
	}
}

// startWaiting shows a "Waiting for <model> " line (no newline) while the
// request is in flight — aider's cue so a slow-to-wake model doesn't look
// hung. clearWaiting erases it before the first output.
func (o *termOutput) startWaiting(name string) {
	o.guard()
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

// Link prints a URL as itself, made clickable where the terminal supports it.
// Stolen from Kimi Code, which hyperlinks the URL in its approval panel.
//
// The escape is the one place output is assembled rather than sanitized, so
// both halves are checked here rather than trusted from the caller. The text is
// sanitized like any other output. The *target* is refused the escape entirely
// if it carries a control byte, which would let it close the OSC and start
// something of its own — the URL comes from the model, and this is the only
// path that puts model-supplied bytes near an escape Strument writes. In
// practice url.Parse has already rejected every ASCII control character before
// a URL can reach here (verified, not assumed); this is the belt to that
// bracing, so the method stays safe for a caller that has not parsed anything.
//
// Off when color is off, which is also when output is being captured — see
// doc/experimenting.md on what Strument's own escape sequences did to a scorer.
func (o *termOutput) Link(target string) {
	o.guard()
	o.clearWaiting()
	text := render.Sanitize(target)
	if !o.color || strings.ContainsFunc(target, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		fmt.Fprint(o.w, text+"\n")
		o.sep.Clear()
		return
	}
	const st = "\x1b\\"
	fmt.Fprint(o.w, "\x1b]8;;"+target+st+text+"\x1b]8;;"+st+"\n")
	o.sep.Clear()
}

// Printf is the REPL's own voice — the banner, the usage line, the /add
// acknowledgements — and everything it says falls outside a step. So it settles
// the group separator rather than owing one: the next turn's first thinking
// must not start with a blank line pushed down from the turn before.
func (o *termOutput) Printf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprint(o.w, render.Sanitize(fmt.Sprintf(format, args...))+"\n")
	o.sep.Clear()
}

func (o *termOutput) Toolf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprint(o.w, o.sgr(o.theme.Tool)+render.Sanitize(fmt.Sprintf(format, args...))+o.sgr("0")+"\n")
	o.sep.Drew()
}

// ToolBlock writes a run_code program as a bracketed block, thinking's shape:
// markers in the recessive tool color, the body plain — the source is content
// the user reads, not narration, and coloring Python by the tool palette would
// fight its own syntax.
func (o *termOutput) ToolBlock(title, body string) {
	o.guard()
	o.clearWaiting()
	var b strings.Builder
	render.ToolBlock(&b, title, body)
	lines := strings.SplitAfter(strings.TrimSuffix(b.String(), "\n"), "\n")
	for i, line := range lines {
		// The delimiters take the tool color; body lines print uncolored. A
		// first/last-line test on the trimmed block tells them apart, since the
		// body is written verbatim and the markers are the frame.
		if i == 0 || i == len(lines)-1 || line == render.CodeClose {
			fmt.Fprint(o.w, o.sgr(o.theme.Tool)+line+o.sgr("0"))
		} else {
			fmt.Fprint(o.w, line)
		}
	}
	fmt.Fprint(o.w, "\n")
	o.sep.Drew()
}

func (o *termOutput) Warningf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprint(o.w, o.sgr(o.theme.Warning)+render.Sanitize(fmt.Sprintf(format, args...))+o.sgr("0")+"\n")
	o.sep.Drew()
}

func (o *termOutput) Errorf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprint(o.w, o.sgr(o.theme.Error)+render.Sanitize(fmt.Sprintf(format, args...))+o.sgr("0")+"\n")
	o.sep.Drew()
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

// StreamReasoning renders the model's thinking. The shape — inline for one
// line, bracketed past that, capped where the user asked — belongs to
// render.Thinking, which script mode uses too; what stays here is the terminal
// half: the dimmed markdown renderer the body goes through.
func (o *termOutput) StreamReasoning(delta string) {
	o.guard()
	o.clearWaiting()
	o.hideCursor()

	if o.phase != phaseReasoning {
		o.phase = phaseReasoning
		// The separator is paid here, at the opening marker, rather than on the
		// first delta. Thinking opens lazily — a block that turns out to be
		// empty never reaches Marker at all — so this is the earliest point at
		// which the block is known to be real, and the last one still above the
		// "‹thinking›" it has to precede.
		firstMarker := true
		o.think = &render.Thinking{
			Marker: func(s string) {
				if firstMarker {
					firstMarker = false
					o.sep.Before(o.w)
				}
				fmt.Fprint(o.w, o.sgr(o.theme.Reasoning)+s+o.sgr("0"))
			},
			Body: func(s string) {
				// Opened lazily and through the recessive palette, so the whole
				// block reads as an aside. streamed goes up here rather than on
				// the first delta: a provider that pads a turn with an empty
				// reasoning delta has streamed nothing, and claiming otherwise
				// buys a blank line with no content above it.
				if o.parser == nil {
					o.parser = render.NewParser(render.NewANSI(o.w, o.color, o.reasoningTheme(), o.width))
					o.streamed = true
					o.sep.Drew()
				}
				o.parser.Write(s)
			},
			CloseBody: o.closeParser,
			Progress: func(s string) {
				// The cap is reached — no more body text will follow — so
				// flush the parser now; otherwise the trailing newline from
				// the last body line arrives after the progress update rather
				// than before it.
				o.closeParser()
				fmt.Fprint(o.w, o.sgr(o.theme.Reasoning)+s+o.sgr("0"))
			},
			Display: o.Thinking,
		}
	}
	o.think.Write(render.Sanitize(delta))
}

// endReasoning closes the thinking block and reports whether there was one.
func (o *termOutput) endReasoning() bool {
	if o.phase != phaseReasoning || o.think == nil {
		return false
	}
	rendered := o.think.End()
	o.think = nil
	o.phase = phaseNone
	return rendered
}

// separateFromAnswer puts one blank line between a thinking block and the
// answer that follows it.
//
// This is the one place a separator still goes *after* the thinking, and the
// asymmetry is deliberate. Thinking heads the tool calls it explains, so a call
// follows it directly; an answer is a different kind of thing, and running the
// two together would blur the line between what the model worked out and what
// it decided to tell the user. Everything else is spaced by render.GroupSep,
// before the next block rather than after this one.
func (o *termOutput) separateFromAnswer() {
	fmt.Fprintln(o.w)
	o.sep.Clear() // the blank just written is the group's, so nothing is owed
}

func (o *termOutput) StreamText(delta string) {
	o.guard()
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
		o.held.Write(render.Sanitize(delta))
		return
	}

	o.clearWaiting()
	o.hideCursor()
	if o.phase == phaseReasoning && o.endReasoning() {
		o.separateFromAnswer()
	}
	o.phase = phaseAnswer
	o.ensureParser()
	o.streamed = true
	o.sep.Drew()
	o.parser.Write(render.Sanitize(delta))
}

func (o *termOutput) StreamToolCall(index int, name, args string) {
	o.guard()
	o.clearWaiting()
	o.hideCursor()
	// A tool-call turn ends the markdown answer (finish_reason: tool_calls),
	// so close the parser before the diff begins; they never interleave.
	if o.phase == phaseReasoning {
		// Reasoning gave way to the calls it was about. No separator: the
		// thinking heads this group, and GroupSep will space it from the last
		// one. The streamed flag is cleared by hand because thinking sets it,
		// and a send whose calls all draw nothing — a read or a grep, printing
		// its outcome later through Toolf — would otherwise leave FlushStream a
		// blank line to put directly above that outcome.
		if o.endReasoning() {
			o.streamed = false
		}
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
	o.guard()
	o.clearWaiting()
	o.endReasoning() // a send that was nothing but thinking still closes it
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
		o.sep.Clear() // this blank is the gap; the next group need not repeat it
	}
	o.phase = phaseNone
	o.answerVisible = false
	o.toolStarted = false
	o.showCursor()
}

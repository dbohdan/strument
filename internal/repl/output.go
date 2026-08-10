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

	// thinkHeld carries the first line of a thinking block until it is known
	// whether there will be a second; thinkOpened says the marker is out, and
	// thinkBlock that it went on a line of its own and wants a closer.
	thinkHeld   strings.Builder
	thinkOpened bool
	thinkBlock  bool
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
	for _, c := range p[:n] {
		if c == '\n' {
			g.run++
		} else {
			g.run = 0
		}
	}
	return n, err
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

func (o *termOutput) Printf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprintf(o.w, format+"\n", args...)
}

func (o *termOutput) Toolf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprintf(o.w, o.sgr(o.theme.Tool)+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) Warningf(format string, args ...any) {
	o.guard()
	o.clearWaiting()
	fmt.Fprintf(o.w, o.sgr(o.theme.Warning)+format+o.sgr("0")+"\n", args...)
}

func (o *termOutput) Errorf(format string, args ...any) {
	o.guard()
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

// The delimiters around the model's thinking. Tag-shaped and deliberately not
// tag-valid: stripReasoning (coder/send.go) removes <tag>…</tag> from model
// output, so printing that shape would make the harness's voice
// indistinguishable from the model's, and a transcript would round-trip
// lossily through Strument itself. The guillemets are General Punctuation, the
// same block as the "…" already used by the diff renderer, so they need no font
// beyond what the elision marker already assumes.
//
// Two constants so the glyphs can be swapped in one line while the shape stays
// put; the shape lives in StreamReasoning and endReasoning.
const (
	thinkingOpen  = "‹thinking›"
	thinkingClose = "‹/›"
)

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

// StreamReasoning renders the model's thinking behind a delimiter rather than a
// banner, and picks its shape from the thinking itself: one line of it reads as
// a prefixed aside, several lines as a bracketed block.
//
// Most thinking is one line. In a seven-step session, five of the seven blocks
// were a single sentence restating the tool call that immediately followed —
// "Let me check the output.go file", then Read output.go. A banner cannot be a
// prefix, which is why that shape had to go rather than merely shrink.
//
// The shape cannot be decided as it streams, because by the time a newline
// arrives the marker is already on screen. So the first line is held and
// nothing more: at most one line of latency, after which a long block streams
// live as it is generated. Which line the text lands on is decided by the
// text's own newlines, which is sound because the renderer does not wrap —
// render.ANSI uses its width only for rule length.
func (o *termOutput) StreamReasoning(delta string) {
	o.guard()
	o.clearWaiting()
	o.hideCursor()

	if o.phase != phaseReasoning {
		o.phase = phaseReasoning
		o.thinkHeld.Reset()
		o.thinkOpened, o.thinkBlock = false, false
	}

	if !o.thinkOpened {
		o.thinkHeld.WriteString(delta)
		// Trimmed at both ends before looking: a newline the provider puts
		// before or after the thinking is spacing, not a second line. Only an
		// interior one means the block runs past one line.
		if !strings.Contains(strings.TrimSpace(o.thinkHeld.String()), "\n") {
			return // one line so far, and it may stay that way
		}
		o.openThinking(true)
		return // openThinking released the held text, delta included
	}
	o.parser.Write(delta)
}

// openThinking emits the opening marker and releases the held first line. block
// says whether the thinking runs past one line, which decides whether the
// marker takes a line of its own.
func (o *termOutput) openThinking(block bool) {
	o.thinkOpened, o.thinkBlock = true, block
	// Set here rather than on the first delta: a provider that pads a turn with
	// an empty reasoning delta has streamed nothing, and claiming otherwise buys
	// a blank line from FlushStream with no content above it.
	o.streamed = true
	dim, off := o.sgr(o.theme.Reasoning), o.sgr("0")
	if block {
		fmt.Fprintf(o.w, "%s%s%s\n", dim, thinkingOpen, off)
	} else {
		fmt.Fprintf(o.w, "%s%s%s ", dim, thinkingOpen, off)
	}
	o.parser = render.NewParser(render.NewANSI(o.w, o.color, o.reasoningTheme(), o.width))
	o.parser.Write(strings.TrimLeft(o.thinkHeld.String(), " \t\r\n"))
	o.thinkHeld.Reset()
}

// separateFromThinking puts one blank line between the thinking and whatever
// follows, and takes the streamed flag with it.
//
// That second part is the whole point. The flag exists so FlushStream can put a
// blank line after anything the stream drew, and thinking sets it — so when the
// next thing draws nothing of its own (a read or a grep, which print their
// outcome later through Toolf), FlushStream would add a second blank under this
// one. This separator has already done that job, so it clears the debt; whatever
// writes next sets the flag again.
func (o *termOutput) separateFromThinking() {
	fmt.Fprintln(o.w)
	o.streamed = false
}

// endReasoning closes the thinking block and reports whether there was one. A
// one-liner needs no closing marker: the line it sits on is the whole of it.
func (o *termOutput) endReasoning() bool {
	if o.phase != phaseReasoning {
		return false
	}
	if !o.thinkOpened && strings.TrimSpace(o.thinkHeld.String()) != "" {
		o.openThinking(false) // the block ended on its first line
	}
	rendered := o.thinkOpened
	o.closeParser()
	if o.thinkBlock {
		fmt.Fprintf(o.w, "%s%s%s\n", o.sgr(o.theme.Reasoning), thinkingClose, o.sgr("0"))
	}
	o.thinkHeld.Reset()
	o.thinkOpened, o.thinkBlock = false, false
	o.phase = phaseNone
	return rendered
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
		o.held.Write(delta)
		return
	}

	o.clearWaiting()
	o.hideCursor()
	if o.phase == phaseReasoning && o.endReasoning() {
		o.separateFromThinking()
	}
	o.phase = phaseAnswer
	o.ensureParser()
	o.streamed = true
	o.parser.Write(delta)
}

func (o *termOutput) StreamToolCall(index int, name, args string) {
	o.guard()
	o.clearWaiting()
	o.hideCursor()
	// A tool-call turn ends the markdown answer (finish_reason: tool_calls),
	// so close the parser before the diff begins; they never interleave.
	if o.phase == phaseReasoning {
		if o.endReasoning() { // reasoning gave way to edits: close the block
			o.separateFromThinking()
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
	}
	o.phase = phaseNone
	o.answerVisible = false
	o.toolStarted = false
	o.showCursor()
}

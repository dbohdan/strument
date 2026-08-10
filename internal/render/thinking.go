package render

import (
	"fmt"
	"io"
	"strings"
)

// The delimiters around the model's thinking. Tag-shaped and deliberately not
// tag-valid: stripReasoning (coder/send.go) removes <tag>…</tag> from model
// output, so printing that shape would make the harness's voice
// indistinguishable from the model's, and a transcript would round-trip
// lossily through Strument itself. The guillemets are General Punctuation, the
// same block as the "…" the diff renderer already uses, so they need no font
// beyond what the elision marker already assumes.
//
// Two constants so the glyphs can be swapped in one line while the shape stays
// put; the shape lives in Thinking below.
const (
	ThinkingOpen  = "‹thinking›"
	ThinkingClose = "‹/›"
)

// ThinkingMode is what a Thinking does with a block. It mirrors
// config.ReasoningMode, which the caller translates — render does not import
// config, and this is the whole of what it needs to know.
type ThinkingMode int

const (
	ThinkingFull ThinkingMode = iota
	ThinkingCapped
	ThinkingOff
)

// Thinking renders one reasoning block, and owns the part both outputs agree
// on: whether the block is inline or bracketed, how much of it to show, and
// where the markers go.
//
// It lives here rather than in the REPL because script mode needs the same
// shape with different rendering — the terminal routes the text through a
// dimmed markdown renderer, a redirected run writes it plain — and two copies
// of this would drift the first time either was touched.
//
// Most thinking is one line. In a seven-step session, five of seven blocks were
// a single sentence restating the tool call that immediately followed: "Let me
// check the output.go file", then Read output.go. So one line renders as a
// prefixed aside and several as a bracketed block, and the shape is read off
// the thinking itself.
type Thinking struct {
	// Marker writes a delimiter — dimmed in the terminal, plain elsewhere.
	Marker func(string)
	// Body writes the thinking text, through whatever renderer the caller uses.
	Body func(string)
	// CloseBody flushes that renderer and leaves the cursor on a fresh line. It
	// has to exist because Marker writes straight to the terminal while Body may
	// go through a buffering markdown renderer: anything the marker emits before
	// the body is closed arrives in the wrong place.
	CloseBody func()
	// Display is how much to show.
	Display ThinkingDisplay

	held    strings.Builder
	opened  bool
	block   bool
	lines   int  // lines of body emitted so far
	elided  int  // lines withheld by the cap
	stopped bool // the cap is reached; count the rest, render none of it
}

// ThinkingDisplay is the caller's answer to "how much of it".
type ThinkingDisplay struct {
	Mode  ThinkingMode
	Lines int // meaningful only for ThinkingCapped
}

// Write feeds the next reasoning delta.
//
// The shape cannot be decided as it streams: by the time a newline arrives the
// marker is already out. So the first line is held and nothing more — at most
// one line of latency, after which a long block streams live as it is
// generated. Newlines at either end are the provider's spacing rather than a
// second line, so only an interior one makes a block. Reading the text's own
// newlines is sound because the renderer does not wrap: ANSI uses its width for
// rule length alone.
func (t *Thinking) Write(delta string) {
	if t.Display.Mode == ThinkingOff {
		return
	}
	if !t.opened {
		t.held.WriteString(delta)
		if !strings.Contains(strings.TrimSpace(t.held.String()), "\n") {
			return // one line so far, and it may stay that way
		}
		t.open(true)
		return // open released the held text, delta included
	}
	t.emit(delta)
}

// open emits the marker and releases the held first line. block says the
// thinking runs past one line, which decides whether the marker takes a line of
// its own.
func (t *Thinking) open(block bool) {
	t.opened, t.block = true, block
	if block {
		t.Marker(ThinkingOpen + "\n")
	} else {
		t.Marker(ThinkingOpen + " ")
	}
	t.emit(strings.TrimLeft(t.held.String(), " \t\r\n"))
	t.held.Reset()
}

// emit writes body text, stopping at the cap and counting what it skips.
//
// A cap keeps the *first* lines, and that is the useful half rather than merely
// the streamable one. A block usually ends by restating its conclusion, which
// the answer then says again; what appears nowhere else is the opening — the
// approach weighed, the option rejected. (Keeping the last N would also mean
// buffering the whole block, leaving the screen blank through a long think.)
func (t *Thinking) emit(s string) {
	if s == "" {
		return
	}
	if t.Display.Mode != ThinkingCapped {
		t.Body(s)
		t.lines += strings.Count(s, "\n")
		return
	}
	for s != "" {
		line, rest, found := strings.Cut(s, "\n")
		if t.stopped {
			// A trailing piece with no newline is still a line — the last one,
			// which a block need not terminate. Counting only on the newline
			// undercounted every block by one, and by *all* of them when the cap
			// fell on the first line.
			if found || line != "" {
				t.elided++
			}
			if !found {
				break
			}
			s = rest
			continue
		}
		if found {
			t.Body(line + "\n")
			t.lines++
			if t.lines >= t.Display.Lines {
				t.stopped = true
			}
			s = rest
			continue
		}
		t.Body(line)
		s = ""
	}
}

// End closes the block and reports whether there was one. A one-liner needs no
// closing marker: the line it sits on is the whole of it.
//
// Everything the marker writes comes after CloseBody, without exception. The
// body may be going through a renderer that buffers; the markers are not. Emit
// one before the other has been flushed and it lands in the middle of the text
// it was meant to follow.
func (t *Thinking) End() bool {
	if t.Display.Mode == ThinkingOff {
		t.reset()
		return false
	}
	if !t.opened && strings.TrimSpace(t.held.String()) != "" {
		t.open(false) // the block ended on its first line
	}
	rendered := t.opened
	if t.CloseBody != nil {
		t.CloseBody()
	}
	if rendered && t.elided > 0 {
		// The diff renderer's idiom, so an elision reads as native rather than
		// invented.
		t.Marker(fmt.Sprintf("… %d more %s of thinking …\n", t.elided, plural(t.elided, "line", "lines")))
	}
	if rendered && t.block {
		t.Marker(ThinkingClose + "\n")
	}
	t.reset()
	return rendered
}

func (t *Thinking) reset() {
	t.held.Reset()
	t.opened, t.block, t.stopped = false, false, false
	t.lines, t.elided = 0, 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// PlainThinking builds a Thinking that writes markers and body straight to w,
// with no color and no markdown rendering — the shape a redirected run gets.
//
// It still has to keep the fresh-line promise CloseBody makes, since a block
// need not end in a newline and the closing marker wants its own line. Nothing
// buffers here, so tracking the last byte written is the whole of it.
func PlainThinking(w io.Writer, display ThinkingDisplay) *Thinking {
	atLineStart := true
	write := func(s string) {
		if s == "" {
			return
		}
		fmt.Fprint(w, s)
		atLineStart = strings.HasSuffix(s, "\n")
	}
	t := &Thinking{Marker: write, Body: write, Display: display}
	t.CloseBody = func() {
		if !atLineStart {
			write("\n")
		}
	}
	return t
}

package coder

import (
	"strings"
	"unicode"
)

// Degenerate repetition: the failure that does not end on its own.
//
// A model stuck calling one tool repeatedly tends to break out by itself, and
// the step budget already caps that. What does not break out is a model
// repeating *text* — "the the the", or one sentence over and over until the
// context fills. Ten such loops from a real aider history, across ten models,
// were all in the reasoning stream and none in an answer, so this watches both
// but expects to earn its keep on the first.
//
// The shape is Gemini CLI's, because it is the only substantial implementation
// in the field and because it scored 7/7 on that corpus while a
// sentence-periodicity detector scored 4/7 and caught nothing the window
// detector missed. script/find-loops.py is what measured it — it runs the same
// four detectors over aider histories and Strument transcripts — and the short
// version is that one detector is enough and this is the one.
//
// Deliberately naive. Every window is hashed by taking a substring, and the map
// grows with the reply. The upgrade, when a profile asks for it, is a rolling
// hash over a ring buffer — h = h*base + in - out*base^(k-1), or buzhash —
// which makes it O(1) per byte with bounded memory and needs no dependency;
// github.com/chmduquesne/rollinghash is the closest thing to a standard package
// and is larger than the fifteen lines it would replace. Measure first: this
// runs over a bounded tail, not the whole reply, so it may never be the
// bottleneck.
const (
	// loopWindow is the width of the repeating unit looked for. Gemini CLI's
	// number. Wide enough that ordinary phrasing does not recur exactly, narrow
	// enough to sit inside one repeated sentence.
	loopWindow = 50
	// loopMinCount is how many times a window must recur before the reply is
	// stopped.
	loopMinCount = 10
	// loopMaxAvgGap bounds the average distance between occurrences, which is
	// what separates a loop from a word that happens to be common. Gemini CLI
	// uses 250. Widened here because the corpus tolerated it: from 250 to 2000
	// the counts did not move — 7 loops found, 0 false positives — and the
	// headroom covers a long-period cycle, which is the one shape a window
	// detector can otherwise miss.
	loopMaxAvgGap = 1000
	// loopTailBytes caps what is kept. A loop is visible in its own recent
	// output; holding a whole reply to find one would be the expensive way to
	// answer a local question.
	loopTailBytes = 16 << 10
	// loopMinWordRun is the other detector, and it is five lines rather than a
	// second algorithm: a stutter is one "sentence" with no terminator, so
	// nothing that splits on prose can see it. "Dynamical" 84 times and a
	// screenful of "!" were two of the ten real loops.
	loopMinWordRun = 30
)

// The two streams a reply arrives on, named because the finding reports which
// one repeated and the wording of the note depends on it.
const (
	loopAnswer    = "answer"
	loopReasoning = "reasoning"
)

// loopFinding says what repeated, for a message the user can judge.
type loopFinding struct {
	// Sample is the repeating text itself, trimmed for display.
	Sample string
	// Count is how many times it occurred.
	Count int
	// Kind is "answer" or "reasoning" — worth reporting, because a model that
	// loops while thinking and then answers cleanly is a different animal.
	Kind string
}

// loopDetector watches one send's streamed text.
//
// Answer and reasoning are tracked separately rather than concatenated. They
// interleave on the wire, and a window spanning the seam between them is not a
// repetition of anything the model wrote.
type loopDetector struct {
	// MinCount is how many times a window must recur, or 0 to detect nothing.
	MinCount int
	// MinRun is the same for an immediately repeated word, or 0 for neither.
	MinRun int
	// Found is the first finding, kept so the caller can quote it after the
	// stream has been abandoned.
	Found *loopFinding

	answer, reasoning loopStream
}

// loopStream is one side's buffer plus the state needed to strip quoted
// material as it arrives. Line-oriented, because that is the unit everything
// below classifies on, and a stream event is not a line: it is a token or a
// few, so a partial line has to be held back until its newline turns up.
type loopStream struct {
	kept    strings.Builder // already stripped
	pending string          // the incomplete last line
	inFence bool
}

// newLoopDetector returns a detector with the tuned thresholds, or one that
// never fires. `loop_detection = False` in the config is what turns it off; a
// model whose ordinary output trips this should not have to wait for a fix.
func newLoopDetector(on bool) *loopDetector {
	if !on {
		return &loopDetector{}
	}
	return &loopDetector{MinCount: loopMinCount, MinRun: loopMinWordRun}
}

// add appends streamed text, keeping only what is the model's own prose.
//
// Quoted material is blanked rather than removed, which matters: dropping the
// lines outright would pull distant repeats together and put them inside
// loopMaxAvgGap, inventing the loop the strip is meant to prevent. A run of
// newlines is harmless because findLoop already refuses a window that is mostly
// whitespace.
func (s *loopStream) add(text string) {
	s.pending += text
	for {
		i := strings.IndexByte(s.pending, '\n')
		if i < 0 {
			return
		}
		line := s.pending[:i]
		s.pending = s.pending[i+1:]
		if s.keepLine(line) {
			s.kept.WriteString(line)
		}
		s.kept.WriteByte('\n')
	}
}

// tail is what the detectors see: the kept lines, plus the line still being
// written when it is not inside a fence. The pending line has to be included or
// a stutter with no newline in it -- "Dynamical" 84 times on one line was a real
// one -- would sit in the buffer forever and never be looked at.
func (s *loopStream) tail() string {
	if s.inFence || s.pending == "" {
		return s.kept.String()
	}
	return s.kept.String() + s.pending
}

// keepLine reports whether a line is the model's own prose.
//
// This is script/find-loops.py's strip_code_blocks, which is the same transform
// the offline tool has always applied before running these same detectors --
// and the divergence was the bug. The tool that TUNED these thresholds strips
// quoted code; the in-process detector did not, so it fired on a model reading
// a file back to itself. Gemini CLI, whose shape this is, resets on the same
// constructs for the same reason.
//
// Live evidence, not a theory: two of thirty runs in the skills trial were
// stopped mid-turn for quoting the very file they had been asked to edit. The
// fixture's thirteen near-identical <line> elements put a 50-byte window over
// the threshold exactly, and both models had already worked out the right
// answer when the harness cut them off.
func (s *loopStream) keepLine(line string) bool {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
		s.inFence = !s.inFence
		return false
	}
	if s.inFence {
		return false
	}
	// A table row repeats its own punctuation by construction.
	if strings.HasPrefix(t, "|") {
		return false
	}
	// Unfenced markup and data. A fence is the common case and the one both
	// observed failures took, but nothing obliges a model to use one, and a
	// line that is a whole element or a whole object field is not prose under
	// any reading.
	if len(t) > 1 && t[0] == '<' && t[len(t)-1] == '>' {
		return false
	}
	if isDataField(t) {
		return false
	}
	// Scaffolding around a fence: aider heads every SEARCH/REPLACE block with a
	// bare filename, so an answer carrying forty edits repeats "main.go" forty
	// times once the fences are gone.
	//
	// It has to look like a *path*, not merely be short and spaceless. The
	// first version of this in find-loops.py dropped every short spaceless line
	// and deleted a real finding with it -- a token-level stutter renders one
	// word per line, and "Dynamical" repeated 84 times vanished. That is why
	// the rune check below is here and why TestStripKeepsAOneWordPerLineStutter
	// exists.
	if isBarePath(t) {
		return false
	}
	return true
}

// isBarePath matches the filename that heads an edit block.
//
// The separator has to sit *between* characters. Requiring only that one be
// present anywhere deleted "Yes." -- short, spaceless, ends in a dot -- and
// with it any one-word-per-line stutter whose word carries punctuation, which
// is the very shape this transform must never eat.
func isBarePath(t string) bool {
	if t == "" || len(t) >= 40 || strings.ContainsAny(t, " \t") {
		return false
	}
	if strings.ContainsAny(t, "/\\") {
		return true
	}
	i := strings.IndexByte(t, '.')
	return i > 0 && i < len(t)-1
}

// isDataField matches a JSON or YAML field line: `"key": value,` or
// `key: value`.
//
// Narrow on purpose, because the failure mode of this whole function is
// deleting prose and thereby hiding a real loop. The quoted form needs its
// quotes. The bare form needs an identifier key AND a single-token value:
// without that second condition it swallowed "Note: the following is wrong"
// and "Then: I will read it again", which are sentences, not fields.
func isDataField(t string) bool {
	t = strings.TrimSpace(strings.TrimLeft(t, "{[-"))
	if strings.HasPrefix(t, `"`) {
		i := strings.Index(t, `":`)
		return i > 0 && !strings.ContainsAny(t[1:i], " \t")
	}
	i := strings.IndexByte(t, ':')
	if i <= 0 || i == len(t)-1 {
		return false
	}
	key, value := t[:i], strings.TrimSpace(t[i+1:])
	if !strings.ContainsFunc(key, unicode.IsLetter) {
		return false
	}
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return len(strings.Fields(value)) == 1
}

// feed adds streamed text and reports a loop when one is visible.
//
// Called per stream event, which is the right granularity: an event is a token
// or a few, so a loop is caught within a window of its becoming detectable
// rather than at the end of the reply.
func (d *loopDetector) feed(kind, text string) *loopFinding {
	if d == nil || text == "" || (d.MinCount <= 0 && d.MinRun <= 0) {
		return nil
	}
	b := &d.answer
	if kind == loopReasoning {
		b = &d.reasoning
	}
	b.add(text)
	if b.kept.Len() > loopTailBytes {
		// Keep the tail. Trimming loses a loop that started earlier and would
		// still be running, but a running loop re-establishes itself inside the
		// window within loopMinCount repetitions.
		s := b.kept.String()
		b.kept.Reset()
		b.kept.WriteString(s[len(s)-loopTailBytes:])
	}
	tail := b.tail()
	f := findLoop(tail, d.MinCount)
	if f == nil {
		f = findWordRun(tail, d.MinRun)
	}
	if f == nil {
		return nil
	}
	f.Kind = kind
	if d.Found == nil {
		d.Found = f
	}
	return f
}

// findLoop reports a window that recurs often enough, at close enough spacing,
// to be a loop rather than a habit of phrasing.
func findLoop(text string, minCount int) *loopFinding {
	if minCount <= 0 || len(text) < loopWindow*minCount {
		return nil
	}
	// Positions bucketed by window content. The naive part: this allocates a
	// substring per offset and a map that grows with the tail. See the note at
	// the top for what replaces it if a profile ever complains.
	pos := make(map[string][]int, len(text)/2)
	for i := 0; i+loopWindow <= len(text); i++ {
		w := text[i : i+loopWindow]
		pos[w] = append(pos[w], i)
		p := pos[w]
		if len(p) < minCount {
			continue
		}
		// A window of nothing but whitespace recurs forever and means nothing.
		if len(strings.TrimSpace(w)) < loopWindow/4 {
			continue
		}
		gaps := p[len(p)-1] - p[0]
		if float64(gaps)/float64(len(p)-1) > loopMaxAvgGap {
			continue
		}
		return &loopFinding{Sample: sampleOf(w), Count: len(p)}
	}
	return nil
}

// findWordRun reports a word repeated immediately, many times over.
//
// Separate from findLoop because the two see different things: a stutter short
// enough to fit inside one window is invisible to a window detector unless the
// window happens to align, and this is cheaper than widening that one.
func findWordRun(text string, minRun int) *loopFinding {
	if minRun <= 0 {
		return nil
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	run, prev := 0, ""
	for _, w := range words {
		if strings.EqualFold(w, prev) {
			run++
		} else {
			run, prev = 1, w
		}
		// A repeated number is a grid, not a stutter: thirty zeroes in a row is
		// what printing a sparse matrix looks like. Requiring a letter costs
		// nothing real — the loops this catches are words.
		if run >= minRun && strings.ContainsFunc(prev, unicode.IsLetter) {
			return &loopFinding{Sample: sampleOf(prev), Count: run}
		}
	}
	return nil
}

// sampleOf trims a repeating unit for a one-line message, with the newlines
// made visible so a two-line unit does not silently become one.
func sampleOf(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", "⏎")
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}

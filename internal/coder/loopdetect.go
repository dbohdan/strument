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

	answer, reasoning strings.Builder
}

// newLoopDetector returns a detector with the tuned thresholds, or one that
// never fires. `detect_loops = False` in the config is what turns it off; a
// model whose ordinary output trips this should not have to wait for a fix.
func newLoopDetector(on bool) *loopDetector {
	if !on {
		return &loopDetector{}
	}
	return &loopDetector{MinCount: loopMinCount, MinRun: loopMinWordRun}
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
	b.WriteString(text)
	if b.Len() > loopTailBytes {
		// Keep the tail. Trimming loses a loop that started earlier and would
		// still be running, but a running loop re-establishes itself inside the
		// window within loopMinCount repetitions.
		s := b.String()
		b.Reset()
		b.WriteString(s[len(s)-loopTailBytes:])
	}
	tail := b.String()
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

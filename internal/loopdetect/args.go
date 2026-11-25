package loopdetect

import "strings"

// Arguments is the third stream: a tool call's arguments as they arrive.
const Arguments = "arguments"

// Tool-call arguments are mostly code and data, which repeat on purpose: a
// table of test cases, a board of identical rows, a run of near-identical
// lines. The prose detector blanks quoted code for exactly that reason, so it
// cannot be pointed at a write's content without stopping ordinary files.
// What is never ordinary is a long stretch that is *exactly* periodic. A model
// that has lost its place inside an argument writes the same lines verbatim
// until something stops it: Qwen3-VL 8B wrote three lines 1,045 times in one
// write call and ran to a 30-minute timeout, because nothing watched the
// arguments (doc/experiments/2026-11-small-model-ladder).
//
// So the test here is narrow. The last argSpan bytes of one call's arguments
// must equal themselves shifted by one period, the period no longer than a
// tenth of the span, so a finding means at least ten verbatim copies covering
// 16 KB. Code that does that by hand is generated code a model should write
// with a program, and `loop_detection = False` turns it off.
const (
	argSpan      = 16 << 10
	argMinCount  = 10
	argMaxPeriod = argSpan / argMinCount
	argTailBytes = argSpan + argMaxPeriod
	// argProbe is how much of the tail is looked for earlier on to propose a
	// period. Long enough that ordinary text does not recur by chance at a
	// shorter distance than a real loop's.
	argProbe = 64
	// argCheckEvery spaces the checks out: each costs a search and a compare
	// over the span, and a loop of this size is not in a hurry to be caught.
	argCheckEvery = 1 << 10
)

// argStream is one tool call's argument tail.
type argStream struct {
	tail       string
	sinceCheck int
}

// FeedArgs adds streamed argument text for the tool call at index and reports
// a loop when one is visible. Calls are kept apart: parallel calls may
// interleave on the wire, and a seam between two is not a repetition.
func (d *Detector) FeedArgs(index int, text string) *Finding {
	if d == nil || text == "" || d.MinCount <= 0 {
		return nil
	}
	if d.args == nil {
		d.args = map[int]*argStream{}
	}
	s := d.args[index]
	if s == nil {
		s = &argStream{}
		d.args[index] = s
	}
	s.tail += text
	if len(s.tail) > argTailBytes {
		s.tail = strings.Clone(s.tail[len(s.tail)-argTailBytes:])
	}
	s.sinceCheck += len(text)
	if s.sinceCheck < argCheckEvery {
		return nil
	}
	s.sinceCheck = 0
	p := period(s.tail)
	if p == 0 {
		return nil
	}
	f := &Finding{Sample: sampleOf(lineStart(unescapeJSON(s.tail[len(s.tail)-p:]))), Count: argSpan / p, Kind: Arguments}
	if d.Found == nil {
		d.Found = f
	}
	return f
}

// period returns the shortest p ≤ argMaxPeriod for which the last argSpan bytes
// of s equal the argSpan bytes p earlier, or 0 if there is none. Candidates
// come from earlier occurrences of the last argProbe bytes, nearest first.
func period(s string) int {
	n := len(s)
	if n < argSpan+1 {
		return 0
	}
	probe := s[n-argProbe:]
	span := s[n-argSpan:]
	limit := n - 1 // the earlier occurrence must end before the probe does
	for {
		j := strings.LastIndex(s[:limit], probe)
		if j < 0 {
			return 0
		}
		p := n - argProbe - j
		if p > argMaxPeriod || n-argSpan-p < 0 {
			return 0
		}
		if span == s[n-argSpan-p:n-p] {
			return p
		}
		limit = j + argProbe - 1
	}
}

// unescapeJSON undoes the escapes that matter for reading a sample. The
// arguments stream as a JSON document, so a repeated line arrives as `x\n`
// with a literal backslash.
func unescapeJSON(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(s)
}

// lineStart rotates one period of a loop to begin at a line, so the sample
// quotes whole lines rather than starting wherever the stream happened to be.
func lineStart(unit string) string {
	i := strings.IndexByte(unit, '\n')
	if i < 0 || i == len(unit)-1 {
		return unit
	}
	return unit[i+1:] + unit[:i+1]
}

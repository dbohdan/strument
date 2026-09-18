package coder

import (
	"fmt"
	"slices"
	"strings"
)

// Two run_code ergonomics arms, under trial —
// doc/experiments/2026-09-run-code-ergonomics/.
//
// Both answer frictions observed in live sessions rather than imagined ones,
// and both are description changes, which is what makes them a trial rather
// than a fix: CLAUDE.md treats model-facing text as a prompt change.
//
// What the 2026-09 namespace trial established has to be read against them
// first. It found that the mistake a description is meant to prevent is made in
// the *first* program — 46% of first programs, 10% of second, ~0% after — and
// concluded that nothing in a tool description reaches a habit executed that
// early. That is a prediction of failure for anything in this file, and the
// reason the trial bins its counts by program position.
//
// The two arms are not equally exposed to it. CodeSignatures documents an
// order, which competes with a habit (write Python the way Python is written)
// and is the arm the namespace finding predicts will do nothing. CodeReadText
// offers a function that does not otherwise exist, which is not a habit to
// overcome but a fact the model cannot know — and a model that discovers
// read's formatting the hard way adapts within the session, which is a
// different curve from one that keeps reaching for os.walk.

// CodeSignatures adds the bridged tools' positional signatures to the run_code
// description.
//
// The gap is real and narrow. In the shipped arrangement the model is offered
// read, grep, glob, ls and symbol as direct tools in the same request, so it has
// their parameter *names* from those schemas. What no schema can carry is the
// *order*: ToolDef.Parameters is a map, and what reaches the model is Go's
// marshalling of it, sorted alphabetically. Until codeToolParams was written
// there was no order to document; now there is one, and nothing tells the model
// what it is.
type CodeSignatures bool

// CodeReadText registers read_text(path), which returns a file's text as
// stored.
//
// The friction is observed: read's result inside a program is the formatted
// tool output — a "README.md (7 lines)" header and a "N\t" prefix per line — so
// a program computing over file contents measures the harness's formatting
// unless it strips it first. DeepSeek V4.1 Flash spent two programs on exactly
// that in a live run and said so itself: "my first two attempts were measuring
// the tool's own formatting rather than the file".
//
// The counter-metric this arm needs is its own downside. read's numbering is
// load-bearing when the answer is a citation — "which line is this on" — so a
// model that reaches for read_text by default trades a wrong measurement for a
// missing reference. The trial counts both.
type CodeReadText bool

// codeSignatureText renders the signature paragraph from codeToolParams, so the
// documented order and the registered one are the same list rather than two
// that agree today.
func codeSignatureText(callable []string, ns CodeNamespace, readText CodeReadText) string {
	sigs := make([]string, 0, len(callable)+1)
	for _, name := range callable {
		params, ok := codeToolParams[name]
		if !ok {
			continue
		}
		sigs = append(sigs, fmt.Sprintf("%s(%s)", nsQualify(name, ns), strings.Join(params, ", ")))
	}
	for _, d := range codeFuncsFor(readText) {
		sigs = append(sigs, fmt.Sprintf("%s(%s)", d.name, strings.Join(d.params, ", ")))
	}
	if len(sigs) == 0 {
		return ""
	}
	return "\n\nArguments may be given by position, in this order: " + strings.Join(sigs, ", ") +
		". Keywords work too, and are clearer past the first argument."
}

// codeFuncsFor is the registry the arm selects. The base registry plus
// read_text when the arm is on.
func codeFuncsFor(readText CodeReadText) []codeFuncDef {
	if !readText {
		return codeFuncs
	}
	return append(slices.Clone(codeFuncs), readTextFunc)
}

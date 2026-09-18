package coder

import (
	"fmt"
	"strings"
)

// The run_code ergonomics trial's surviving arm —
// doc/experiments/2026-09-run-code-ergonomics/README.md.
//
// The trial ran two. read_text won decisively and is shipped unconditionally
// (codefuncs.go). This one, documenting the bridged tools' positional order in
// the description, did not: 19/27 against the baseline's 14/27, p = 0.26, and
// combined with read_text it was the *worse* of the two — 24/27 where read_text
// alone scored 27/27, because the extra paragraph cost three uses of the
// function that was doing the work. Not significant in either direction, so the
// honest reading is "no demonstrated benefit", and the flag stays off.
//
// Kept rather than deleted because the question is not closed: the arm was
// built for a gap that shrank underneath it. Positional arguments now bind
// (codeToolParams), so what a signature line still buys is the order of the
// second and later arguments — which the tasks here barely exercised, since
// almost every call a model made took one.

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

// codeSignatureText renders the signature paragraph from codeToolParams, so the
// documented order and the registered one are the same list rather than two
// that agree today.
func codeSignatureText(callable []string, ns CodeNamespace) string {
	sigs := make([]string, 0, len(callable)+1)
	for _, name := range callable {
		params, ok := codeToolParams[name]
		if !ok {
			continue
		}
		sigs = append(sigs, fmt.Sprintf("%s(%s)", nsQualify(name, ns), strings.Join(params, ", ")))
	}
	for _, d := range codeFuncs {
		sigs = append(sigs, fmt.Sprintf("%s(%s)", d.name, strings.Join(d.params, ", ")))
	}
	if len(sigs) == 0 {
		return ""
	}
	return "\n\nArguments may be given by position, in this order: " + strings.Join(sigs, ", ") +
		". Keywords work too, and are clearer past the first argument."
}

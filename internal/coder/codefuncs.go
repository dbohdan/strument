package coder

// This file holds the code functions — functions callable from inside a
// run_code program only, and from nowhere else. They are deliberately not
// tools: the model never sees their names in the tool schema, and a direct
// tool call of one of their names hits Inspector.Run's "Unknown tool" branch,
// because that switch does not know them either. The single source of truth
// is codeFuncs below; registration with Monty, dispatch in the bridge, and
// the tool description all read from it, so the three cannot drift.
//
// Their contract differs from the observation tools' in the two ways that
// matter. First, the result is *data for the program to compute over*, not
// prose for the model — the program's conclusion, not the raw payload, is
// what reaches the conversation. Second, errors are Go errors, which the
// bridge turns into Monty exceptions naming the program line that made the
// call — the prefix-classified sentences the inspector tools use would be
// the wrong shape here, since these never pass through bridgeToolFailure.

import (
	"errors"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/monty"
)

// codeFuncDef is one run_code-only function.
type codeFuncDef struct {
	name string
	// summary is the one line the tool description carries. It must state the
	// signature and the return shape, because the description is the only
	// place the model learns either.
	summary string
	// params is the positional order of the arguments, which must match the
	// signature the summary states: the summary is the only place the model
	// learns it, and Monty drops a positional it has no name for. See
	// codeToolParams.
	params []string
	// fn receives the decoded arguments and returns data. The Coder is passed
	// so a function can reach c.Files and c.Root — the same fields an
	// Inspector is built from — and containment is each function's own
	// business; the mechanism provides access, not authority.
	fn func(c *Coder, call *monty.FunctionCall) (any, error)
}

// codeFuncs is the registry. The only place a run_code-only function is
// named. Ordered for reading, not looked up by position — codeFuncByName
// scans.
var codeFuncs = []codeFuncDef{
	{
		name: "read_bin",
		summary: "read_bin(path, offset=0, limit=4096) reads a window of a file's raw bytes as " +
			"{size, offset, truncated, data} where data is a list of 0-255 ints. For computing " +
			"over binary files (magic numbers, entropy, embedded strings); read is the " +
			"text-shaped one and refuses binaries.",
		params: []string{"path", "offset", "limit"},
		fn:     runReadBin,
	},
}

// codeFuncByName returns the registry entry for name, or nil.
func codeFuncByName(readText CodeReadText, name string) *codeFuncDef {
	defs := codeFuncsFor(readText)
	for i := range defs {
		if defs[i].name == name {
			return &defs[i]
		}
	}
	return nil
}

// codeFuncDoc renders the registry into the run_code description: one line per
// function, appended after the bridged-tools list. Built from the registry so
// description and dispatch cannot drift — the drift this repository has had
// three times elsewhere.
func codeFuncDoc(readText CodeReadText) string {
	defs := codeFuncsFor(readText)
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nAlso callable, but only from inside a program (they return data for " +
		"the program, not text for you):\n")
	for _, d := range defs {
		fmt.Fprintf(&b, "- %s\n", d.summary)
	}
	return b.String()
}

// runReadBin answers one read_bin call from a program. The window defaults to
// 4 KiB and is capped at 64 KiB by workspace.ReadBytes itself; each call
// counts against the bridge's call cap, which bridgeCall enforces before this
// is reached.
func runReadBin(c *Coder, call *monty.FunctionCall) (any, error) {
	path, _ := call.Args["path"].(string)
	if path == "" {
		return nil, errors.New("read_bin requires a \"path\" argument")
	}
	offset := codeArgInt(call.Args["offset"])
	limit := codeArgInt(call.Args["limit"])

	fb, err := c.Files.ReadBytes(path, offset, limit)
	if err != nil {
		// The tool's own error sentence, as an exception — Monty's traceback
		// then names the program line that made the call. Capitalized on
		// purpose: it continues Monty's "external function … failed:" frame,
		// and a lowercase sentence there reads as a fragment. Wrapped with %w
		// so the underlying cause stays unwrappable.
		return nil, fmt.Errorf("Could not read %s: %w", quoteToolArg(path), err) //nolint:staticcheck // ST1005, see above
	}
	data := make([]any, len(fb.Data))
	for i, b := range fb.Data {
		data[i] = int(b)
	}
	return map[string]any{
		"size":      fb.Size,
		"offset":    fb.Offset,
		"truncated": fb.Truncated,
		"data":      data,
	}, nil
}

// codeArgInt pulls an optional integer argument out of decoded JSON. JSON
// numbers arrive as float64; a wrong-typed argument is a zero, and the
// downstream clamp treats that as "default". Never use it for a required
// argument — a required one must fail loudly, as runReadBin does for path.
func codeArgInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

// maxReadTextLines bounds a whole-file read_text. Larger than any file the
// workspace will open by line count — its byte cap binds first — so reaching it
// means a pathological file rather than an ordinary one, and reaching it raises
// rather than truncating.
const maxReadTextLines = 1_000_000

// readTextFunc is the CodeReadText arm's function. Kept out of codeFuncs so the
// registry stays the shipped set and the arm is the only thing that adds it;
// codeFuncsFor assembles the list the description and the bridge both use.
var readTextFunc = codeFuncDef{
	name: "read_text",
	summary: "read_text(path, offset=0, limit=0) returns a file's text exactly as stored — no " +
		"line numbers, no header — for computing over contents (lengths, parsing, hashing, " +
		"counting). read is the one to use when the answer cites a line number.",
	params: []string{"path", "offset", "limit"},
	fn:     runReadText,
}

// runReadText answers one read_text call.
//
// It goes through Workspace.Read, so the containment and ignore rules that bind
// every other way of opening a file bind this one too; the only difference from
// the read tool is that the tool layer's numbering and header are not applied.
//
// A window that stops short raises rather than returning a partial file. The
// whole purpose here is computing over contents, and a length or a count taken
// from silently truncated text is a wrong answer that looks like a right one —
// the failure this function exists to remove, reintroduced one layer down.
func runReadText(c *Coder, call *monty.FunctionCall) (any, error) {
	path, _ := call.Args["path"].(string)
	if path == "" {
		return nil, errors.New("read_text requires a \"path\" argument")
	}
	offset := int(codeArgInt(call.Args["offset"]))
	limit := int(codeArgInt(call.Args["limit"]))
	if limit <= 0 {
		// The whole file. Workspace refuses one larger than its byte cap before
		// this is reached, so "all of it" is already bounded.
		limit = maxReadTextLines
	}
	ft, err := c.Files.Read(path, offset, limit)
	if err != nil {
		//nolint:staticcheck // ST1005: continues Monty's "external function … failed:" frame.
		return nil, fmt.Errorf("Could not read %s: %w", quoteToolArg(path), err)
	}
	if ft.Truncated {
		//nolint:staticcheck // ST1005, as above.
		return nil, fmt.Errorf("Could not read all of %s: it has %d lines and the window stopped at %d; "+
			"pass offset and limit to take it in pieces", quoteToolArg(path), ft.Total, len(ft.Lines))
	}
	return strings.Join(ft.Lines, "\n"), nil
}

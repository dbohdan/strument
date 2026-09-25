package coder

// This file holds the code functions — functions callable from inside a
// run_code program only, and from nowhere else. They are deliberately not
// tools: the model never sees their names in the tool schema, and a direct
// tool call of one of their names hits Inspector.Run's "Unknown tool" branch,
// because that switch does not know them either. The single source of truth
// is codeFuncs below; registration in the interpreter, dispatch in the bridge, and
// the tool description all read from it, so the three cannot drift.
//
// Their contract differs from the observation tools' in the two ways that
// matter. First, the result is *data for the program to compute over*, not
// prose for the model — the program's conclusion, not the raw payload, is
// what reaches the conversation. Second, errors are Go errors, which the
// bridge throws as Errors at the program line that made the
// call — the prefix-classified sentences the inspector tools use would be
// the wrong shape here, since these never pass through bridgeToolFailure.

import (
	"errors"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/workspace"
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
	// learns it, and a positional past the end of it is dropped. See
	// codeToolParams.
	params []string
	// fn receives the decoded arguments and returns data. The Coder is passed
	// so a function can reach c.Files and c.Root — the same fields an
	// Inspector is built from — and containment is each function's own
	// business; the mechanism provides access, not authority.
	fn func(c *Coder, call *bridgedCall) (any, error)
}

// codeFuncs is the registry. The only place a run_code-only function is
// named. Ordered for reading, not looked up by position — codeFuncByName
// scans.
var codeFuncs = []codeFuncDef{
	{
		name: "read_bin",
		summary: "read_bin({path, offset: 0, limit: 4096}) reads a window of a file's raw bytes as " +
			"{size, offset, truncated, data} where data is an array of 0-255 numbers. For computing " +
			"over binary files (magic numbers, entropy, embedded strings); read is the " +
			"text-shaped one and refuses binaries.",
		params: []string{"path", "offset", "limit"},
		fn:     runReadBin,
	},
	readTextFunc,
}

// codeDataFuncs are the bridged tools that exist in two shapes. Inside a
// program the data shape wins, because a program computes over a result and
// the tool shape is prose for the model: glob's answer names the pattern and
// explains glob syntax, and sorted() over that prose iterates its characters —
// a live session turned one such call into 49 junk tool calls under the
// 50-call cap, and the model's own diagnosis was that the interpreter was
// broken. The shapes cannot collide at a call site: the bridge dispatches on
// this registry before Inspector.Run, and a direct tool call never crosses the
// bridge. The registered list, the bridge's dispatch, and the description's
// "returns data" sentence all read from codeDataFuncs, so the three cannot
// drift.
//
// Errors stay empty results rather than failures, matching the prose shape:
// "No files match" is an answer a program filters on, so it arrives as an
// empty list.
var codeDataFuncs = []codeFuncDef{
	{
		name: "glob",
		summary: "glob({pattern}) returns the matching project-relative paths as an array of strings, " +
			"empty when nothing matches. " +
			"The pattern is matched against the whole path, segment by segment; \"**/*.go\" reaches " +
			"every directory, \"*.go\" only the root, and a bare directory name matches nothing.",
		params: []string{"pattern"},
		fn:     runGlobData,
	},
	{
		name: "grep",
		summary: "grep({pattern, path, glob, mode, ignore_case, context_lines}) returns, by mode: " +
			"\"files\" (the default) an array of paths; \"count\" an array of {path, count}; " +
			"\"content\" an array of {path, line, text, match} for the matching lines, with " +
			"context_lines adding the lines around them as match: false. A line's text is clipped " +
			"at 200 bytes, ending in \" …\"; read_text has the whole file.",
		params: codeToolParams[toolGrep],
		fn:     runGrepData,
	},
	{
		name: "ls",
		summary: "ls({path: \"\"}) returns one directory's entries as an array of objects {path, is_dir, link} " +
			"sorted by path. Empty path is the " +
			"project root; a directory under the standard temp directory is allowed too. " +
			"link is the symlink target, present only on symlinks.",
		params: []string{"path"},
		fn:     runLSData,
	},
}

// codeFuncByName returns the registry entry for name, or nil.
func codeFuncByName(name string) *codeFuncDef {
	return codeFuncScan(codeFuncs, name)
}

// codeDataFuncByName returns the data-shape entry for name, or nil. Separate
// from codeFuncByName so a future name that exists in both shapes is a
// decision, not an accident of scan order.
func codeDataFuncByName(name string) *codeFuncDef {
	return codeFuncScan(codeDataFuncs, name)
}

func codeFuncScan(defs []codeFuncDef, name string) *codeFuncDef {
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
func codeFuncDoc() string {
	defs := codeFuncs
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

// codeDataFuncDoc renders the data shapes into the description, with the
// overriding stated in the header: these names are the tools', so the program
// gets the list, not the tool's prose. Built from the registry like
// codeFuncDoc, for the same reason.
func codeDataFuncDoc() string {
	var b strings.Builder
	b.WriteString("\n\nInside a program, glob, grep and ls return data rather than the tools' " +
		"prose, and the other tools return the same text they return to you:\n")
	for _, d := range codeDataFuncs {
		fmt.Fprintf(&b, "- %s\n", d.summary)
	}
	return b.String()
}

// runReadBin answers one read_bin call from a program. The window defaults to
// 4 KiB and is capped at 64 KiB by workspace.ReadBytes itself; each call
// counts against the bridge's call cap, which bridgeCall enforces before this
// is reached.
func runReadBin(c *Coder, call *bridgedCall) (any, error) {
	path, _ := call.Args["path"].(string)
	if path == "" {
		return nil, errors.New("read_bin requires a \"path\" argument")
	}
	offset := codeArgInt(call.Args["offset"])
	limit := codeArgInt(call.Args["limit"])

	fb, err := c.Files.ReadBytes(path, offset, limit)
	if err != nil {
		// The tool's own error sentence, thrown at the program line that made
		// the call. Capitalized on purpose: it is a sentence the model reads
		// whole, and a lowercase one reads as a fragment. Wrapped with %w so
		// the underlying cause stays unwrappable.
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
	case int64:
		// What goja exports an integral JavaScript number as.
		return n
	}
	return 0
}

// maxReadTextLines bounds a whole-file read_text. Larger than any file the
// workspace will open by line count — its byte cap binds first — so reaching it
// means a pathological file rather than an ordinary one, and reaching it raises
// rather than truncating.
const maxReadTextLines = 1_000_000

// readTextFunc answers the friction that read's own result is the *formatted*
// tool output — a header and an "N\t" prefix per line — so a program computing
// over a file's contents measures the harness unless it strips that first.
//
// Shipped after a trial rather than on the strength of the story:
// doc/experiments/2026-09-run-code-ergonomics/README.md. 144 runs, and every
// run that called this function answered correctly (51/51) against 14/27 for
// the arm without it, p = 0.000036 — at 1.6 steps per turn rather than 2.7, and
// for less money. Its own counter-metric came back clean: on the one task where
// read's line numbers *are* the answer, no model reached for this instead.
var readTextFunc = codeFuncDef{
	name: "read_text",
	summary: "read_text({path, offset: 0, limit: 0}) returns a file's text exactly as stored — no " +
		"line numbers, no header — for computing over contents (lengths, parsing, hashing, " +
		"counting). It keeps the file's final newline, so text.split(\"\\n\") ends with an empty " +
		"string; drop it before counting lines. read is the one to use when the answer cites " +
		"a line number.",
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
func runReadText(c *Coder, call *bridgedCall) (any, error) {
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
		//nolint:staticcheck // ST1005: a sentence the model reads whole, as the tools' own errors are.
		return nil, fmt.Errorf("Could not read %s: %w", quoteToolArg(path), err)
	}
	if ft.Truncated {
		//nolint:staticcheck // ST1005, as above.
		return nil, fmt.Errorf("Could not read all of %s: it has %d lines and the window stopped at %d; "+
			"pass offset and limit to take it in pieces", quoteToolArg(path), ft.Total, len(ft.Lines))
	}
	text := strings.Join(ft.Lines, "\n")
	// The terminator splitLines removed, when this window reached the end of
	// the file. Without it the text is not what is stored, and a file whose
	// last line is blank loses that line entirely — which is a wrong count
	// that looks like a right one, the exact failure this function exists to
	// remove. Found in the trial's pilot, by a model that did everything right.
	if ft.EndsWithNewline && !ft.Truncated && len(ft.Lines) > 0 {
		text += "\n"
	}
	return text, nil
}

// runGlobData answers a glob call from a program with the paths as data. It
// runs the same Workspace.Glob the tool runs, so containment, ignore rules,
// and the results limit are shared; only the rendering differs.
//
// The return is the bare array, not an object carrying a truncation flag
// beside it: a program iterating the result would iterate the object's keys
// and get ["paths"] — the same quiet wrong-shape iteration the prose
// shape produced, one level down. Truncation instead raises, naming the
// repair: the tool's 1,000-path limit is real, and a program computing over
// a silently cut list would take a wrong answer for a right one.
func runGlobData(c *Coder, call *bridgedCall) (any, error) {
	pattern, _ := call.Args["pattern"].(string)
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("glob requires a \"pattern\" argument")
	}
	paths, trunc, err := c.Files.Glob(pattern)
	if err != nil {
		//nolint:staticcheck // ST1005: a sentence the model reads whole, as the tools' own errors are.
		return nil, fmt.Errorf("Could not match %s: %w", quoteToolArg(pattern), err)
	}
	if trunc.Any() {
		return nil, fmt.Errorf("glob matched at least %d paths, past the limit; narrow the pattern "+
			"— with a directory path in grep, or a **/sub/ pattern — and work on a subtree",
			len(paths))
	}
	// A nil slice becomes null in the program —
	// and a no-match result then looks exactly like the discarded-results
	// failure the note is for. Empty is a value; make it the value it claims
	// to be.
	if paths == nil {
		paths = []string{}
	}
	return paths, nil
}

// runGrepData answers a grep call from a program with the matches as data.
//
// grep's prose was the last one a program had to parse, and parsing it is
// where programs went wrong: MiMo, splitting count-mode output on ":", took
// the header line "485 matches in 53 files:" for a file and spent a second
// program repairing its parser. glob and ls became data for the same reason.
//
// Content mode defaults to no context here, where the tool defaults to two
// lines: a program wants the matches, and context rows it did not ask for are
// rows it must remember to filter out. Asked for, they come as match: false.
//
// Two results raise instead of returning, each naming the repair. A cut
// result, for glob's reason: a program computing over it would take a wrong
// answer for a right one. And a scope that admitted no files at all, which
// the tool explains in prose: an empty array there would read as "not
// found" when the pattern was never tested.
func runGrepData(c *Coder, call *bridgedCall) (any, error) {
	str := func(k string) string { s, _ := call.Args[k].(string); return s }
	pattern := str("pattern")
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("grep requires a \"pattern\" argument")
	}
	mode := workspace.GrepFiles
	switch str("mode") {
	case "", "files":
	case "content":
		mode = workspace.GrepContent
	case "count":
		mode = workspace.GrepCount
	default:
		return nil, fmt.Errorf("unknown grep mode %q; use \"files\", \"content\", or \"count\"", str("mode"))
	}
	context := int(codeArgInt(call.Args["context_lines"]))
	if context < 0 || context > maxGrepContext {
		return nil, fmt.Errorf("context_lines must be between 0 and %d", maxGrepContext)
	}
	if context > 0 {
		mode = workspace.GrepContent
	}
	ignoreCase, _ := call.Args["ignore_case"].(bool)
	res, err := c.Files.Grep(workspace.GrepQuery{
		Pattern: pattern, Glob: str("glob"), Dir: str("path"), Mode: mode,
		IgnoreCase: ignoreCase, ContextLines: context,
	})
	if err != nil {
		//nolint:staticcheck // ST1005: a sentence the model reads whole, as the tools' own errors are.
		return nil, fmt.Errorf("The search pattern was not valid: %w", err)
	}
	if res.InScope == 0 {
		return nil, fmt.Errorf("grep searched no files: nothing is in that scope, so the pattern was "+
			"never tested. %s", globSyntaxNote)
	}
	if res.Truncated.Any() {
		return nil, fmt.Errorf("grep matched past its limit (%d lines in %d files so far); narrow it "+
			"with glob or path and work on a part at a time", res.Total, len(res.Files))
	}
	out := make([]any, 0, len(res.Files))
	for _, f := range res.Files {
		switch mode {
		case workspace.GrepFiles:
			out = append(out, f.Path)
		case workspace.GrepCount:
			out = append(out, map[string]any{"path": f.Path, "count": f.Count})
		case workspace.GrepContent:
			for _, l := range f.Lines {
				out = append(out, map[string]any{"path": f.Path, "line": l.Number, "text": l.Text, "match": l.Match})
			}
		}
	}
	return out, nil
}

// runLSData answers an ls call from a program with the entries as data. Same
// containment as the tool, including the temp-directory exemption, which is
// what the live session's program was actually after when it went through
// glob: listing cloned repositories under /tmp.
func runLSData(c *Coder, call *bridgedCall) (any, error) {
	dir, _ := call.Args["path"].(string)
	entries, total, err := c.Files.List(dir)
	if err != nil {
		//nolint:staticcheck // ST1005: a sentence the model reads whole, as the tools' own errors are.
		return nil, fmt.Errorf("Could not list %s: %w", quoteToolArg(dir), err)
	}
	// Raised rather than returned short, for the reason glob raises: a
	// program computing over a silently cut list takes a wrong answer for a
	// right one.
	if total > len(entries) {
		return nil, fmt.Errorf("ls found %d entries in %s, past its limit of %d; list a subdirectory, "+
			"or match part of it with glob", total, displayDir(dir), len(entries))
	}
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		m := map[string]any{"path": e.Path, "is_dir": e.IsDir}
		if e.Link != "" {
			m["link"] = e.Link
		}
		out = append(out, m)
	}
	return out, nil
}

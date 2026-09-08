package coder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CodeNamespace is how a run_code program reaches the tools: as bare names, as
// a `tools` namespace, or both. Under trial — doc/experiments/2026-09-code-namespace/README.md.
//
// The target is a failure with a measured rate rather than an imagined one. Over
// 530 programs from the code-result trial's saved sessions, 56 (10.6%) reached
// for Python's own filesystem — `import os`, `open(`, `import glob`, `pathlib` —
// and 45 of 108 sessions wasted at least one program that way. MiMo does it in
// 17% of its programs. Every one of those is a round trip spent discovering that
// this interpreter has no filesystem, in a session whose tool description
// already says so in prose.
//
// The hypothesis under test is that a named namespace is a better signpost than
// a paragraph: `tools.read(path=…)` says "this environment provides its own I/O"
// in the shape of the call itself, where prose has to be believed. Monty has no
// `dir()`, `vars()`, `globals()` or `__dict__` — all probed against the vendored
// wasm — so the introspection half of that idea is unavailable, and `print(tools)`
// through a `__str__` is the closest thing there is.
type CodeNamespace int

const (
	// CodeNSFlat is the shipped arrangement: read(), grep(), glob(), ls().
	CodeNSFlat CodeNamespace = iota
	// CodeNSBoth adds a `tools` object beside the bare names, so nothing that
	// worked stops working.
	CodeNSBoth
	// CodeNSOnly withholds the bare names, so `read(...)` raises NameError and
	// the namespace is the only way through. The forcing version, and the one
	// that can make things worse.
	CodeNSOnly
	// CodeNSHint keeps the flat names and instead answers a reach for Python's
	// filesystem with the tool that serves the need. Not the namespace idea at
	// all: it treats the failure as a signposting problem at the moment of the
	// mistake rather than at the moment of the description.
	CodeNSHint
)

func (n CodeNamespace) String() string {
	switch n {
	case CodeNSBoth:
		return "both"
	case CodeNSOnly:
		return "only"
	case CodeNSHint:
		return "hint"
	default:
		return "flat"
	}
}

// ParseCodeNamespace reads an arm name, reporting whether it is one.
func ParseCodeNamespace(name string) (CodeNamespace, bool) {
	switch name {
	case "flat":
		return CodeNSFlat, true
	case "both":
		return CodeNSBoth, true
	case "only":
		return CodeNSOnly, true
	case "hint":
		return CodeNSHint, true
	}
	return 0, false
}

// CodeNamespaceNames lists the arms, for help text and errors.
var CodeNamespaceNames = []string{"flat", "both", "only", "hint"}

// nsPrefix is what a bridged name is registered as when the bare names are
// withheld. The program never sees it: the prelude binds tools.read = _read,
// and the bridge strips it before dispatch.
const nsPrefix = "_"

// wireName is the name a tool is registered with in Monty for this arm.
func (n CodeNamespace) wireName(tool string) string {
	if n == CodeNSOnly {
		return nsPrefix + tool
	}
	return tool
}

// toolName undoes wireName.
func (n CodeNamespace) toolName(wire string) string {
	if n == CodeNSOnly {
		return strings.TrimPrefix(wire, nsPrefix)
	}
	return wire
}

// codePrelude builds the `tools` namespace, or "" when the arm does not use one.
//
// A class rather than a module or a SimpleNamespace, because Monty has neither.
// The __str__ is the whole of the introspection available: print(tools) lists
// the calls, since dir() does not exist.
func codePrelude(arm CodeNamespace, tools []string) string {
	if arm != CodeNSBoth && arm != CodeNSOnly {
		return ""
	}
	var b strings.Builder
	b.WriteString("class _Tools:\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "    %s = %s\n", t, arm.wireName(t))
	}
	b.WriteString("    def __str__(self):\n        return \"")
	sigs := make([]string, 0, len(tools))
	for _, t := range tools {
		sigs = append(sigs, t+"("+codeToolSignature(t)+")")
	}
	b.WriteString(strings.Join(sigs, ", "))
	b.WriteString("\"\n")
	b.WriteString("tools = _Tools()\n")
	return b.String()
}

// codeToolSignature is the argument list print(tools) shows. Short on purpose:
// the schema carries the full contract, and this exists so a model that has
// forgotten an argument name has somewhere cheap to look.
func codeToolSignature(tool string) string {
	switch tool {
	case toolRead:
		return "path, offset, limit"
	case toolGrep:
		return "pattern, glob, path, mode"
	case toolGlob:
		return "pattern"
	case toolLS:
		return "path"
	case toolSymbol:
		return "name, kind"
	}
	return ""
}

// preludeLines counts the lines a prelude adds, for renumbering tracebacks.
func preludeLines(prelude string) int {
	if prelude == "" {
		return 0
	}
	return strings.Count(prelude, "\n")
}

// tracebackLine matches Monty's line reference so a prelude can be subtracted
// back out. Without this, every error a model reads names a line eight past the
// one it wrote, which is worse than no line at all.
var tracebackLine = regexp.MustCompile(`(File "script\.py", line )(\d+)`)

func renumberTraceback(msg string, offset int) string {
	if offset == 0 {
		return msg
	}
	return tracebackLine.ReplaceAllStringFunc(msg, func(m string) string {
		g := tracebackLine.FindStringSubmatch(m)
		n, err := strconv.Atoi(g[2])
		if err != nil || n <= offset {
			return m
		}
		return g[1] + strconv.Itoa(n-offset)
	})
}

// reachedForPython matches the failure this is all about: a program that went
// looking for Python's own filesystem instead of the tools.
var reachedForPython = regexp.MustCompile(
	`No module named '(os|glob|pathlib|subprocess|shutil|io|tempfile)'` +
		`|module 'os' has no attribute` +
		`|module 'pathlib' has no attribute` +
		`|name 'open' is not defined` +
		`|OS call`)

// codeReachHint is what the hint arm appends to such a failure. It names the
// tool that serves the need rather than restating the prohibition, which is the
// shape doc/experiments/2026-08-symbol-uptake/README.md found moves behaviour.
const codeReachHint = "\n\nThis interpreter has no filesystem of its own. " +
	"glob(pattern=\"**/*.py\") walks the tree, ls(path=\".\") lists a directory, " +
	"read(path=\"a.py\") opens a file, and the bash tool runs commands."

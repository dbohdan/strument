package repomap

import (
	"errors"
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"
	"time"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// parseBudget bounds one call. Two things made this necessary, and a sweep over
// the Go and Python standard libraries found both.
//
// Cost: the check runs twice per edited file, before and after, so its latency
// lands on the user's turn. A clean file parses at roughly half a megabyte a
// second, which is nothing for ordinary source; a large generated one is
// seconds.
//
// Correctness: a file the grammar cannot handle is *also* slow, because error
// recovery explores alternatives. cmd/compile/internal/ssa/regalloc.go is 89 KiB
// of valid Go and takes 3.7 seconds — twenty times the rate of a file the
// grammar likes — and comes back wrongly reported as broken. So the same budget
// that bounds the wait also filters much of what the check gets wrong.
//
// Exceeding it yields known=false: nothing is claimed about the file, which is
// the direction this check already fails in when no grammar covers a file.
const parseBudget = 500 * time.Millisecond

// ParseStatus reports whether src parses cleanly under the grammar implied by
// fname's extension. line is the 1-based line of the first parse error, 0 when
// there is none. known is false when no grammar covers the file — the caller
// then knows nothing, which is different from knowing the file is fine.
//
// It exists for the harness's after-an-edit check, so it takes bytes rather
// than a path: the content to judge is what an edit composed, which may not be
// on disk yet, and re-reading would race with the write.
//
// A grammar is enough here. langFor also insists on a tags query, because a
// file with no query cannot contribute to the map; a file with no query still
// parses.
func ParseStatus(fname string, src []byte) (clean bool, line int, known bool) {
	if strings.HasSuffix(fname, ".go") {
		return goParseStatus(fname, src)
	}
	lang := filenameToLang(fname)
	if lang == "" {
		return false, 0, false
	}
	gname := lang
	if n, ok := grammarName[lang]; ok {
		gname = n
	}
	reg := grammars.DetectLanguageByName(gname)
	if reg == nil {
		return false, 0, false
	}

	parser := ts.NewParser(reg.Language())
	parser.SetTimeoutMicros(uint64(parseBudget / time.Microsecond))
	tree, err := parser.Parse(src)
	if err != nil || tree == nil {
		// The parser gave up entirely. That is a fact about the parser, not
		// about the file, so report it as unknown rather than as an error in
		// the user's code.
		return false, 0, false
	}
	if tree.ParseStoppedEarly() {
		// Out of budget. The tree is a partial one and its error nodes describe
		// where the parser ran out of time, not where the code is wrong.
		return false, 0, false
	}
	root := tree.RootNode()
	if root == nil {
		return false, 0, false
	}
	if !root.HasError() {
		return true, 0, true
	}
	return false, firstErrorLine(root), true
}

// firstErrorLine returns the 1-based line of the first ERROR or MISSING node in
// document order, or the root's line when the tree says it has an error but no
// node owns it — the caller always gets somewhere to point.
//
// It walks the whole tree rather than following HasError down. That flag is not
// reliably set on every ancestor in this binding: descending by it reported the
// enclosing function's line for an error two lines inside it. A full walk of one
// file's tree costs nothing next to the edit that prompted it.
func firstErrorLine(root *ts.Node) int {
	var walk func(n *ts.Node) int
	walk = func(n *ts.Node) int {
		if n.IsError() || n.IsMissing() {
			return int(n.StartPoint().Row) + 1
		}
		for i := range n.ChildCount() {
			if child := n.Child(i); child != nil {
				if line := walk(child); line > 0 {
					return line
				}
			}
		}
		return 0
	}
	if line := walk(root); line > 0 {
		return line
	}
	return int(root.StartPoint().Row) + 1
}

// goParseStatus answers for Go with the compiler's own front end.
//
// This is the language Strument is written in and the one it is most used on,
// and go/parser beats the grammar at it on every axis that matters here. It is
// exact — "parses" and "is valid Go" are the same question, so a false positive
// is not a thing it can produce — and it is two to three orders of magnitude
// faster: cmd/compile/internal/ssa/regalloc.go took 3.7 seconds under
// tree-sitter and reported valid code as broken; here it is 3 milliseconds and
// correct. It also gives an exact position rather than the start of an error
// region, because a recovering parser guesses where to resume and this one does
// not have to.
//
// The grammar stays for every other language, and for the repo map and symbol,
// which need the tag queries. This is only about "does it parse".
func goParseStatus(fname string, src []byte) (clean bool, line int, known bool) {
	// SkipObjectResolution: the question is syntax, and resolving identifiers to
	// declarations is work the answer does not depend on.
	_, err := parser.ParseFile(token.NewFileSet(), fname, src, parser.SkipObjectResolution)
	if err == nil {
		return true, 0, true
	}
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return false, list[0].Pos.Line, true
	}
	// A parse error the package did not report as a positioned list. It is still
	// an error; point at the top of the file rather than claiming a line.
	return false, 1, true
}

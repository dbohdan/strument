package repomap

import (
	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

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

	tree, err := ts.NewParser(reg.Language()).Parse(src)
	if err != nil || tree == nil {
		// The parser gave up entirely. That is a fact about the parser, not
		// about the file, so report it as unknown rather than as an error in
		// the user's code.
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

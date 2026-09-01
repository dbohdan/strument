// Defining the shape of a plain-text source file: the top-level definitions
// tree-sitter's tags queries find, with the lines they span.

package repomap

import (
	"sort"
	"strings"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
)

// DefOutline is one top-level definition in a fetched source file, located by
// the lines it spans. The line numbers are what a webfetch range fetches by,
// so they are 1-based and inclusive — the coordinate system of the outline
// and the one the fetch takes are the same, which is what keeps a model from
// translating between the two.
type DefOutline struct {
	Name  string // the identifier as written
	Kind  string // "function", "method", "class", "constant", ... — the query's node kind
	Start int    // first line, 1-based
	End   int    // last line, 1-based, inclusive
}

// DefOutlines extracts a fetched file's top-level definitions with the same
// grammars and tags queries the repo map uses, keyed by the extension of
// fname. known is false when no grammar covers the extension or the source
// does not parse; the caller then falls back to plain text, because a wrong
// map is worse than no map.
//
// It exists for webfetch's outline of a non-HTML page, where the file lives
// at a URL rather than in the repository: the URL's path supplies the
// extension, the body supplies the source, and nothing is read from disk.
// The parse is bounded the way ParseStatus is not — a fetched file is parsed
// once, on demand, so the latency lands on one tool call rather than on every
// edit — but a grammar that would mis-handle the file still gets the budget,
// because error recovery is slow in the same way.
func DefOutlines(fname string, src []byte) (defs []DefOutline, known bool) {
	lang := filenameToLang(fname)
	if lang == "" {
		return nil, false
	}
	entry, err := langFor(lang)
	if err != nil || entry == nil || entry.query == nil {
		return nil, false
	}
	if !utf8.Valid(src) {
		return nil, false
	}
	parser := ts.NewParser(entry.language)
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.RootNode().HasError() {
		// A file with syntax errors still yields captures, but the ranges
		// around the error are the parser's guess rather than the author's
		// structure. A fetched file that does not parse is treated like one
		// with no grammar: plain text, honestly labeled.
		return nil, false
	}

	type rawDef struct {
		name, kind string
		start, end int
		depth      int // tree depth of the definition node
	}
	var raws []rawDef
	// Root depth, so "top-level" can be measured rather than assumed: the
	// program node is the root, but grammars disagree on whether definitions
	// are its direct children or sit one wrapper down (Go's source_file, C#'s
	// namespace). Definitions are kept when their node is within two levels
	// of the root — file scope, or a member of one top-level type/namespace —
	// which is what an outline of a single file is for. Deeper matches are
	// the local variables and parameters the tags queries also name.
	cursor := entry.query.Exec(tree.RootNode(), entry.language, src)
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		for _, c := range m.Captures {
			if !strings.HasPrefix(c.Name, "name.definition.") || c.Node == nil {
				continue
			}
			kind := strings.TrimPrefix(c.Name, "name.definition.")
			// The capture names the identifier; the definition node around it
			// carries the span. Some queries capture the definition node
			// itself, in which case the identifier *is* the span and the
			// parent adds nothing.
			node := c.Node
			start := int(node.StartPoint().Row)
			end := int(node.EndPoint().Row)
			if p := node.Parent(); p != nil && p != node {
				if e := int(p.EndPoint().Row); e > start {
					start, end = int(p.StartPoint().Row), e
				}
			}
			depth := 0
			for n := node; n != nil; n = n.Parent() {
				depth++
			}
			raws = append(raws, rawDef{
				name:  node.Text(src),
				kind:  kind,
				start: start,
				end:   end,
				depth: depth,
			})
		}
	}
	// Measure rather than assume where file scope sits: the shallowest
	// definition found sets the bar, and everything more than a level or two
	// beneath it is a nested or local definition the outline does not list.
	minDepth := 0
	for _, r := range raws {
		if minDepth == 0 || r.depth < minDepth {
			minDepth = r.depth
		}
	}
	kept := raws[:0]
	for _, r := range raws {
		if r.depth <= minDepth+1 {
			kept = append(kept, r)
		}
	}
	raws = kept
	if len(raws) == 0 {
		return nil, true // parses, has a grammar, just nothing at top level
	}

	// One definition per line, ordered as the file presents them. The queries
	// can capture a name twice (a method matched by two patterns); the second
	// copy carries the same span, so it is dropped rather than shown twice.
	sort.SliceStable(raws, func(i, j int) bool {
		if raws[i].start != raws[j].start {
			return raws[i].start < raws[j].start
		}
		return raws[i].name < raws[j].name
	})
	defs = make([]DefOutline, 0, len(raws))
	for i, r := range raws {
		if i > 0 && raws[i-1].name == r.name && raws[i-1].start == r.start {
			continue
		}
		defs = append(defs, DefOutline{
			Name:  r.name,
			Kind:  r.kind,
			Start: r.start + 1,
			End:   r.end + 1,
		})
	}
	return defs, true
}

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
	// Depth is how many definitions enclose this one: 0 at file scope, 1 for
	// a method of a top-level class. Only types enclose anything listed; a
	// function's own definitions are not listed.
	Depth int
	// Signature is the definition's source up to its body, whitespace
	// collapsed: what to recognize it by without reading it.
	Signature string
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
		name, kind   string
		start, end   int // rows
		sb, eb       uint32
		signature    string
		functionLike bool
	}
	var raws []rawDef
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
			// itself, which has children where an identifier has none. This
			// was a rows test (does the parent reach further down?), which
			// took a one-line definition for a bare name.
			node := c.Node
			def := node
			if p := node.Parent(); p != nil && p != node && node.NamedChildCount() == 0 {
				def = p
			}
			raws = append(raws, rawDef{
				name:         node.Text(src),
				kind:         kind,
				start:        int(def.StartPoint().Row),
				end:          int(def.EndPoint().Row),
				sb:           def.StartByte(),
				eb:           def.EndByte(),
				signature:    signatureOf(def, entry.language, src),
				functionLike: kind == "function" || kind == "method",
			})
		}
	}

	// What an outline lists is everything not inside a function: file-scope
	// definitions and the members of types, at any depth of type nesting, but
	// not a function's local helpers. This used to be a depth rule (within one
	// level of the shallowest definition), which grammars disagree about: it
	// kept Go's methods, which sit at file scope, and dropped every Python,
	// TypeScript and Rust method, which sit two or three nodes further down.
	// An outline of a class that names the class and none of its methods is
	// the one a model most needs and least got.
	sort.SliceStable(raws, func(i, j int) bool {
		if raws[i].sb != raws[j].sb {
			return raws[i].sb < raws[j].sb
		}
		return raws[i].eb > raws[j].eb
	})
	defs = make([]DefOutline, 0, len(raws))
	var open []rawDef // enclosing definitions of the current one, outermost first
	for i, r := range raws {
		if i > 0 && raws[i-1].name == r.name && raws[i-1].start == r.start {
			continue // captured twice by two patterns, with the same span
		}
		for len(open) > 0 && open[len(open)-1].eb <= r.sb {
			open = open[:len(open)-1]
		}
		inFunction := false
		for _, o := range open {
			if o.functionLike {
				inFunction = true
			}
		}
		open = append(open, r)
		if inFunction {
			continue
		}
		defs = append(defs, DefOutline{
			Name:      r.name,
			Kind:      r.kind,
			Start:     r.start + 1,
			End:       r.end + 1,
			Depth:     len(open) - 1,
			Signature: r.signature,
		})
	}
	if len(defs) == 0 {
		return nil, true // parses, has a grammar, just nothing at top level
	}
	return defs, true
}

// maxSignature caps one signature in the outline. A signature is for
// recognizing the definition, and a parameter list long enough to hit this
// is still recognizable from its start.
const maxSignature = 120

// signatureOf is a definition's text up to its body — "def fetch(self, url,
// *, timeout=30) -> bytes", "func (c *Coder) runOne(ctx context.Context,
// msg string)" — with whitespace collapsed. A definition without a body
// field (a constant, a type alias, a Go type spec) gives its first line.
// The trailing ":" or "{" that opened the body is dropped.
//
// Generic on purpose: every grammar here names a function's or class's body
// field "body", so one rule covers them without an extractor per language,
// which is what maki keeps 34 of.
func signatureOf(def *ts.Node, lang *ts.Language, src []byte) string {
	start, end := def.StartByte(), def.EndByte()
	if body := def.ChildByFieldName("body", lang); body != nil && body.StartByte() > start {
		// Up to the last child before the body that is not a comment: a
		// comment between a Python def's colon and its block belongs to
		// neither, and read as part of the signature.
		end = start
		for _, ch := range def.Children() {
			if ch.StartByte() >= body.StartByte() {
				break
			}
			if !strings.Contains(ch.Type(lang), "comment") {
				end = ch.EndByte()
			}
		}
	}
	text := string(src[start:end])
	if i := strings.IndexByte(text, '\n'); i >= 0 && end == def.EndByte() {
		text = text[:i]
	}
	// A parameter list laid out one per line collapses to "( self, a, )";
	// the brackets close up the way the one-line form writes them.
	text = strings.Join(strings.Fields(text), " ")
	text = strings.NewReplacer("( ", "(", " )", ")", "[ ", "[", " ]", "]").Replace(text)
	text = strings.NewReplacer(",)", ")", ",]", "]").Replace(text) // a second pass: the first makes these
	text = strings.TrimRight(text, " {:=")
	if utf8.RuneCountInString(text) > maxSignature {
		r := []rune(text)
		text = string(r[:maxSignature-1]) + "…"
	}
	return text
}

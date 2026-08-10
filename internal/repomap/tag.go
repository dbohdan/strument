// Package repomap builds aider's ranked tag map: extract def/ref tags with
// tree-sitter, build a reference graph, run personalized PageRank, and render
// a token-budgeted skeleton of the repository.
package repomap

// Kind distinguishes definition tags from reference tags.
type Kind int

const (
	Def Kind = iota
	Ref
)

// Tag is one extracted identifier occurrence.
type Tag struct {
	RelFname string // repo-root-relative, forward-slashed
	Fname    string // absolute
	Line     int    // 0-based start row; -1 only for chroma-backfilled refs
	Name     string // display identifier text
	Kind     Kind

	// Enclosing names the function this occurrence sits in, "" at file scope or
	// where the extractor cannot say. Only an extractor that knows exactly fills
	// it in — the symbol tool reports it as fact, and a wrong function name
	// sends a reader somewhere real and wrong, which is worse than silence.
	// Empty from tree-sitter; filled by gotags.go.
	Enclosing string
}

// lessTag orders definition tags deterministically before rendering.
func lessTag(a, b Tag) bool {
	if a.RelFname != b.RelFname {
		return a.RelFname < b.RelFname
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.Kind < b.Kind
}

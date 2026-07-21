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
}

// MapItem is a ranked entry: either a full tag or a bare file marker
// (rendered as a filename line with no body).
type MapItem struct {
	RelFname string
	Tag      *Tag // nil => bare file
}

// lessMapItem reproduces Python's tuple ordering over aider's mixed
// (fname,) 1-tuples and 5-field Tag namedtuples: bare sorts before Tag at
// equal RelFname.
func lessMapItem(a, b MapItem) bool {
	if a.RelFname != b.RelFname {
		return a.RelFname < b.RelFname
	}
	if (a.Tag == nil) != (b.Tag == nil) {
		return a.Tag == nil
	}
	if a.Tag == nil {
		return false
	}
	if a.Tag.Line != b.Tag.Line {
		return a.Tag.Line < b.Tag.Line
	}
	if a.Tag.Name != b.Tag.Name {
		return a.Tag.Name < b.Tag.Name
	}
	return a.Tag.Kind < b.Tag.Kind
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

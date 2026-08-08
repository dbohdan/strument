package editblock

// OpKind is how one span of the "before" side relates to the "after" side.
type OpKind int

const (
	// OpEqual: the two spans are identical.
	OpEqual OpKind = iota
	// OpDelete: the "before" span is gone, with nothing in its place.
	OpDelete
	// OpInsert: the "after" span is new, replacing nothing.
	OpInsert
	// OpReplace: the "before" span gave way to the "after" span.
	OpReplace
)

// Op is one span of a line-level diff: a[A1:A2] became b[B1:B2]. For OpEqual
// the two spans have the same length; for OpDelete B1 == B2, and for OpInsert
// A1 == A2.
type Op struct {
	Kind           OpKind
	A1, A2, B1, B2 int
}

// LineOps returns the opcodes describing how the line list a becomes b, in
// order and covering both inputs completely. It ports difflib.get_opcodes over
// the matching blocks the sequence matcher already computes, which is the same
// engine ReplaceMostSimilarChunk and FindSimilarLines run on.
//
// The renderer uses it to show an edit as what actually changed: the edit tool
// asks the model for surrounding context so the search matches uniquely, and
// without a diff every one of those unchanged lines reads as removed and added
// back.
func LineOps(a, b []string) []Op {
	var ops []Op
	i, j := 0, 0
	for _, m := range newSequenceMatcher(a, b).getMatchingBlocks() {
		// Whatever lies between the previous match and this one changed. Which
		// kind it is follows from which side has lines there.
		switch {
		case i < m.A && j < m.B:
			ops = append(ops, Op{OpReplace, i, m.A, j, m.B})
		case i < m.A:
			ops = append(ops, Op{OpDelete, i, m.A, j, m.B})
		case j < m.B:
			ops = append(ops, Op{OpInsert, i, m.A, j, m.B})
		}
		i, j = m.A+m.Size, m.B+m.Size
		if m.Size > 0 {
			ops = append(ops, Op{OpEqual, m.A, i, m.B, j})
		}
	}
	return ops
}

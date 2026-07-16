package editblock

import "sort"

// sequenceMatcher is a port of CPython difflib.SequenceMatcher (Ratcliff/
// Obershelp with junk heuristics), generic over element type. Both editblock
// call sites pass isjunk=None, so only the autojunk heuristic is ported:
// when b has >= 200 elements, elements occurring more than len(b)/100+1
// times cannot seed a match (they are removed from the index) but matches
// still extend across them, exactly as in difflib.
type sequenceMatcher[T comparable] struct {
	a, b []T
	b2j  map[T][]int
}

func newSequenceMatcher[T comparable](a, b []T) *sequenceMatcher[T] {
	m := &sequenceMatcher[T]{a: a}
	m.setSeq2(b)
	return m
}

func (m *sequenceMatcher[T]) setSeq1(a []T) { m.a = a }

func (m *sequenceMatcher[T]) setSeq2(b []T) {
	m.b = b
	m.b2j = make(map[T][]int)
	for i, elt := range b {
		m.b2j[elt] = append(m.b2j[elt], i)
	}
	// autojunk: identical to difflib's popular-element heuristic.
	if n := len(b); n >= 200 {
		ntest := n/100 + 1
		for elt, idxs := range m.b2j {
			if len(idxs) > ntest {
				delete(m.b2j, elt)
			}
		}
	}
}

type match struct{ A, B, Size int }

// findLongestMatch ports difflib.find_longest_match with isjunk=None (the
// bjunk set is always empty, so the two junk-extension loops collapse into
// the plain extension loops).
func (m *sequenceMatcher[T]) findLongestMatch(alo, ahi, blo, bhi int) match {
	besti, bestj, bestsize := alo, blo, 0
	j2len := make(map[int]int)
	for i := alo; i < ahi; i++ {
		newj2len := make(map[int]int)
		for _, j := range m.b2j[m.a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi &&
		m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return match{besti, bestj, bestsize}
}

func (m *sequenceMatcher[T]) getMatchingBlocks() []match {
	la, lb := len(m.a), len(m.b)
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, la, 0, lb}}
	var blocks []match
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		x := m.findLongestMatch(s.alo, s.ahi, s.blo, s.bhi)
		if x.Size > 0 {
			blocks = append(blocks, x)
			if s.alo < x.A && s.blo < x.B {
				queue = append(queue, span{s.alo, x.A, s.blo, x.B})
			}
			if x.A+x.Size < s.ahi && x.B+x.Size < s.bhi {
				queue = append(queue, span{x.A + x.Size, s.ahi, x.B + x.Size, s.bhi})
			}
		}
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].A != blocks[j].A {
			return blocks[i].A < blocks[j].A
		}
		if blocks[i].B != blocks[j].B {
			return blocks[i].B < blocks[j].B
		}
		return blocks[i].Size < blocks[j].Size
	})
	// Merge adjacent blocks.
	var out []match
	var i1, j1, k1 int
	for _, b := range blocks {
		if i1+k1 == b.A && j1+k1 == b.B {
			k1 += b.Size
		} else {
			if k1 > 0 {
				out = append(out, match{i1, j1, k1})
			}
			i1, j1, k1 = b.A, b.B, b.Size
		}
	}
	if k1 > 0 {
		out = append(out, match{i1, j1, k1})
	}
	out = append(out, match{la, lb, 0})
	return out
}

func (m *sequenceMatcher[T]) ratio() float64 {
	matches := 0
	for _, b := range m.getMatchingBlocks() {
		matches += b.Size
	}
	length := len(m.a) + len(m.b)
	if length == 0 {
		return 1.0
	}
	return 2.0 * float64(matches) / float64(length)
}

// stringRatio is difflib's ratio over two strings compared by code point.
func stringRatio(a, b string) float64 {
	return newSequenceMatcher([]rune(a), []rune(b)).ratio()
}

// lineRatio is difflib's ratio over two line lists (elements are whole
// lines), as used by find_similar_lines.
func lineRatio(a, b []string) float64 {
	return newSequenceMatcher(a, b).ratio()
}

// getCloseMatches ports difflib.get_close_matches(word, possibilities, n=1,
// cutoff) far enough for the single-best call site: it returns the best
// possibility with ratio >= cutoff, breaking ratio ties toward the
// lexicographically larger string (matching heapq.nlargest tuple order),
// and false if none qualifies.
func getCloseMatches1(word string, possibilities []string, cutoff float64) (string, bool) {
	wordRunes := []rune(word)
	best := ""
	bestRatio := -1.0
	found := false
	for _, x := range possibilities {
		// difflib: a = candidate possibility, b = word.
		r := newSequenceMatcher([]rune(x), wordRunes).ratio()
		if r < cutoff {
			continue
		}
		if r > bestRatio || (r == bestRatio && x > best) {
			best, bestRatio, found = x, r, true
		}
	}
	return best, found
}

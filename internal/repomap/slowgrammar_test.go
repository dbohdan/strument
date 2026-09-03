package repomap

import "testing"

// slowGrammars names grammars whose *construction* — not their query, and not
// parsing — costs enough to dominate the whole project's test suite, with the
// measurement that put them here.
//
// The cost is in the dependency, in reg.Language(): it gob-decodes a grammar
// blob and interns its transition tables. Swift's blob is 7.5 MB against
// 780 KB for the next largest (Go), and it takes about 12.5 seconds against
// 850 milliseconds for the other thirty-four languages *combined*. A CPU
// profile is all gob decode, transition interning, and the GC pressure of
// both, so there is nothing to fix on this side of the module boundary.
//
// Under -short these are skipped; CI runs the suite without it, so the
// coverage is not lost, only moved off the inner loop. Note the grammar cache
// is a package global, so this has to gate *every* test that reaches such a
// language — whichever one runs first is the one that pays.
var slowGrammars = map[string]string{
	"swift": "~12.5s to build a 7.5 MB grammar blob",
}

// skipSlowGrammar skips the calling test under -short if lang is one of them.
func skipSlowGrammar(t *testing.T, lang string) bool {
	t.Helper()
	if why, ok := slowGrammars[lang]; ok && testing.Short() {
		t.Skipf("skipping %s under -short: %s", lang, why)
		return true
	}
	return false
}

package repomap

import "testing"

// TestAllEmbeddedQueriesCompile guards the embedded tags queries (pack and
// the legacy tree-sitter-languages fallback) against a gotreesitter grammar
// whose node types have diverged: every supported language must compile its
// query. julia and zig are deliberately excluded upstream of this (their
// legacy queries reference node types gotreesitter's grammars lack), so
// they never reach here.
func TestAllEmbeddedQueriesCompile(t *testing.T) {
	langs := SupportedLanguages()
	for _, lang := range langs {
		e, err := langFor(lang)
		if err != nil {
			t.Errorf("%s: %v", lang, err)
			continue
		}
		if e == nil || e.query == nil {
			t.Errorf("%s: no compiled query", lang)
		}
	}
	if len(langs) < 35 {
		t.Errorf("only %d supported languages; expected 28 pack + 7 legacy", len(langs))
	}
}

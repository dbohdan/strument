package repomap

import "testing"

func TestAllEmbeddedQueriesCompile(t *testing.T) {
	langs := SupportedLanguages()
	for _, lang := range langs {
		// Per-language subtests so a failure names the grammar that failed.
		t.Run(lang, func(t *testing.T) {
			e, err := langFor(lang)
			if err != nil {
				t.Fatalf("%s: %v", lang, err)
			}
			if e == nil || e.query == nil {
				t.Fatalf("%s: no compiled query", lang)
			}
		})
	}
	if len(langs) < 35 {
		t.Errorf("only %d supported languages; expected 28 pack + 7 legacy", len(langs))
	}
}

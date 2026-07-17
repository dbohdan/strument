// Keeps script/grammar-tags.txt (the grammar_subset build-tag list used by
// release builds, guide phase 9) in sync with the languages this build
// actually supports. Run under the full build, SupportedLanguages is the
// ground truth; run under the subset build, the same equality proves the
// subset lost nothing.

package repomap

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestGrammarSubsetTagsInSync(t *testing.T) {
	data, err := os.ReadFile("../../script/grammar-tags.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), ",")

	want := []string{"grammar_subset"}
	for _, lang := range SupportedLanguages() {
		gname := lang
		if n, ok := grammarName[lang]; ok {
			gname = n
		}
		want = append(want, "grammar_subset_"+gname)
	}

	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("script/grammar-tags.txt is out of sync with SupportedLanguages:\ngot  %v\nwant %v", got, want)
	}
}

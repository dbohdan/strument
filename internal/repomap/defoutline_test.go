// The def outline of a fetched source file, and the line ranges it yields.

package repomap

import (
	"os"
	"testing"
)

func TestDefOutlinesFindsTopLevelFuncs(t *testing.T) {
	src, err := os.ReadFile("../coder/webfetch.go")
	if err != nil {
		t.Fatal(err)
	}
	defs, known := DefOutlines("x.go", src)
	if !known {
		t.Fatal("a .go file should be known")
	}
	// webfetch.go holds, among others, webfetchTool and truncateFetch.
	var found []string
	for _, d := range defs {
		found = append(found, d.Name)
	}
	for _, want := range []string{"webfetchTool", "truncateFetch", "parseFetchArgs"} {
		ok := false
		for _, n := range found {
			if n == want {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s missing from the outline; got %v", want, found)
		}
	}
	// The spans must be sane: Start <= End, 1-based.
	for _, d := range defs {
		if d.Start < 1 || d.End < d.Start {
			t.Errorf("%s has an impossible range %d-%d", d.Name, d.Start, d.End)
		}
	}
}

func TestDefOutlinesUnknownExtensionIsNotKnown(t *testing.T) {
	_, known := DefOutlines("x.why", []byte("func a() {}\n"))
	if known {
		t.Error("an unknown extension should not claim to know")
	}
}

func TestDefOutlinesUnparseableSourceIsNotKnown(t *testing.T) {
	// Valid Go tokens, invalid Go program: the braces do not balance.
	_, known := DefOutlines("x.go", []byte("func a() {\n"))
	if known {
		t.Error("source that does not parse should not claim to know")
	}
}

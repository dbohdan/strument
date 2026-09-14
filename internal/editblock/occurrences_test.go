package editblock

import (
	"strings"
	"testing"
)

// The file from the field report: a CI matrix whose entries differ only in the
// architecture line above an identical version line. GLM-5.3-Flash was told
// "the text appears 2 times" and resent the byte-identical edit.
const matrix = `      matrix:
        os:
          - name: freebsd
            architecture: x86-64
            version: '13.4'
            host: ubuntu-latest

          - name: freebsd
            architecture: aarch64
            version: '14.3'
            host: ubuntu-latest

          - name: freebsd
            architecture: x86-64
            version: '14.3'
            host: ubuntu-latest
`

func TestOccurrencesShowsWhatDistinguishesTheMatches(t *testing.T) {
	got := Occurrences(matrix, "            version: '14.3'")
	if got == "" {
		t.Fatal("nothing rendered for a text that occurs twice")
	}
	// Both sites, by line number.
	for _, want := range []string{"line 10", "line 15"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// And the lines that actually tell them apart, which is the whole point:
	// a count alone leaves the model to remember the file.
	if !strings.Contains(got, "architecture: aarch64") || !strings.Contains(got, "architecture: x86-64") {
		t.Errorf("the disambiguating context is absent:\n%s", got)
	}
}

// A unique match is not ambiguous and this is never called for one, but it must
// not misreport if it is.
func TestOccurrencesSingleMatch(t *testing.T) {
	got := Occurrences(matrix, "            version: '13.4'")
	if strings.Count(got, "line ") != 1 {
		t.Errorf("want one site:\n%s", got)
	}
	if !strings.Contains(got, "line 5") {
		t.Errorf("wrong line:\n%s", got)
	}
}

func TestOccurrencesAbsentOrEmpty(t *testing.T) {
	if got := Occurrences(matrix, "nothing like this"); got != "" {
		t.Errorf("rendered sites for absent text: %q", got)
	}
	if got := Occurrences(matrix, ""); got != "" {
		t.Errorf("rendered sites for empty search: %q", got)
	}
}

// A match at the very top has no lines above it; the label must not promise
// context it cannot show.
func TestOccurrencesAtFileStart(t *testing.T) {
	got := Occurrences("first\nsecond\nfirst\n", "first")
	if strings.Contains(strings.SplitN(got, "\n", 2)[0], "after") {
		t.Errorf("claimed leading context at the first line:\n%s", got)
	}
}

// Thirty excerpts help nobody: the count plus a narrowing instruction does.
func TestOccurrencesCapsHowManyItShows(t *testing.T) {
	content := strings.Repeat("dup\n", 30)
	got := Occurrences(content, "dup")
	// Count the site labels, not the lines: in a file of repeated text the
	// context lines match the search too, which is what the first version of
	// this assertion counted.
	if n := strings.Count(got, "  line "); n != maxOccurrenceSites {
		t.Errorf("rendered %d sites, want %d:\n%s", n, maxOccurrenceSites, got)
	}
	if !strings.Contains(got, "more") {
		t.Errorf("did not say how many were left out:\n%s", got)
	}
}

// Multi-line search text reports the range it spans, not just its first line.
func TestOccurrencesMultiLineRange(t *testing.T) {
	got := Occurrences(matrix, "            architecture: x86-64\n            version: '14.3'")
	if !strings.Contains(got, "lines 14-15") {
		t.Errorf("want the spanned range:\n%s", got)
	}
}

package editblock

import (
	"fmt"
	"strings"
)

// occurrenceContext is how many lines above each match to show. Two is what
// distinguishes the case this was written for -- a CI matrix whose entries
// differ only in the `architecture:` line above an identical `version:` -- and
// enough to place a match in a file without quoting half of it.
const occurrenceContext = 2

// maxOccurrenceSites caps how many matches are shown. A model that sent text
// occurring thirty times needs to be told the count and to narrow it, not given
// thirty excerpts.
const maxOccurrenceSites = 4

// Occurrences renders where search appears in content, with line numbers and a
// little leading context, so a model told its edit was ambiguous can see what
// would disambiguate it rather than having to remember the file.
//
// The not-found branch of an edit failure has always had FindSimilarLines
// behind it -- "did you mean these lines?" -- while the ambiguous branch had
// only a count. That is backwards: a typo is the easier failure to recover
// from, because the model can re-read and compare, whereas ambiguity needs
// context the model has to reconstruct from a read several messages back. A
// field report has GLM-5.3-Flash resending a byte-identical ambiguous edit
// immediately after being told it was ambiguous, which is what being told a
// number and no location looks like.
//
// Returns "" when there is nothing useful to show.
func Occurrences(content, search string) string {
	if search == "" || !strings.Contains(content, search) {
		return ""
	}
	lines := splitLinesNoEnds(content)
	searchFirst := splitLinesNoEnds(search)
	if len(searchFirst) == 0 {
		return ""
	}

	// Locate by byte offset, then convert to a line number, so a match that
	// starts mid-line is still placed on the line it starts on.
	var starts []int
	for off := 0; ; {
		i := strings.Index(content[off:], search)
		if i < 0 {
			break
		}
		starts = append(starts, off+i)
		off += i + 1 // overlapping sites are separate places; see CountOccurrences

	}

	var b strings.Builder
	shown := 0
	for _, off := range starts {
		if shown == maxOccurrenceSites {
			fmt.Fprintf(&b, "  ... and %d more.\n", len(starts)-shown)
			break
		}
		shown++
		first := strings.Count(content[:off], "\n") // 0-based line of the match
		last := first + len(searchFirst) - 1
		from := max(first-occurrenceContext, 0)
		if last >= len(lines) {
			last = len(lines) - 1
		}
		where := fmt.Sprintf("line %d", first+1)
		if last > first {
			where = fmt.Sprintf("lines %d-%d", first+1, last+1)
		}
		if n := first - from; n > 0 {
			fmt.Fprintf(&b, "  %s, after:\n", where)
		} else {
			fmt.Fprintf(&b, "  %s:\n", where)
		}
		for n := from; n <= last && n < len(lines); n++ {
			fmt.Fprintf(&b, "    %d\t%s\n", n+1, lines[n])
		}
	}
	return b.String()
}

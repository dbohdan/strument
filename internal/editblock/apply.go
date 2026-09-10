// Package editblock is the edit engine: the fuzzy matcher that lands a
// replacement in a file, and the did-you-mean that explains why one didn't.
//
// It used to also parse SEARCH/REPLACE blocks and whole-file listings out of a
// model's prose, and plan a batch of parsed edits. Both went with the text edit
// formats: an edit now arrives as a tool call with a typed path, so there is
// nothing to parse, nothing to re-attribute across files, and no batch report to
// write in SEARCH/REPLACE terms. What survives is the part that was never about
// the format — matching text that a model reproduced imperfectly.
package editblock

import "strings"

// FindSimilarLines ports find_similar_lines: the best window of content
// lines resembling the search lines at ratio >= threshold, expanded by 5
// lines each way unless the endpoints already line up.
//
// Scoring ignores leading whitespace; the lines it returns keep theirs. That
// split is the whole point — a model whose search failed on indentation needs
// to be shown the indentation, and it cannot be shown anything if the search
// never scores above the threshold in the first place.
//
// Upstream compared lines verbatim, which made this blind to the commonest
// way a model gets an edit wrong. A CatchUp session (2026-09-10) recovered a
// bracket-balance block from memory one tab too deep: 13 of its 14 lines were
// in the file modulo indentation and 3 were there verbatim, so the ratio came
// out at 0.143 against a threshold of 0.6 and the model was told only that its
// text was not found. Normalizing takes the same window at the same line to
// 0.929. The window is unchanged and is not the problem: lineRatio is already
// a SequenceMatcher, so it tolerates the lines the model dropped — what it
// could not tolerate was every surviving line differing by a tab.
func FindSimilarLines(search, content string, threshold float64) string {
	searchLines := splitLinesNoEnds(search)
	contentLines := splitLinesNoEnds(content)

	searchKeys := unindent(searchLines)
	contentKeys := unindent(contentLines)

	bestRatio := 0.0
	var bestMatch []string
	bestMatchI := -1

	for i := 0; i+len(searchLines) <= len(contentLines); i++ {
		ratio := lineRatio(searchKeys, contentKeys[i:i+len(searchLines)])
		if ratio > bestRatio {
			bestRatio = ratio
			bestMatch = contentLines[i : i+len(searchLines)]
			bestMatchI = i
		}
	}

	if bestRatio < threshold {
		return ""
	}

	// Endpoints compared the same way they were scored. Indentation alone is
	// not a reason to widen: when the block's boundaries are right and only its
	// depth is wrong, the block itself is the answer, and it carries the depth.
	if len(bestMatch) > 0 && len(searchLines) > 0 &&
		contentKeys[bestMatchI] == searchKeys[0] &&
		contentKeys[bestMatchI+len(bestMatch)-1] == searchKeys[len(searchKeys)-1] {
		return strings.Join(bestMatch, "\n")
	}

	const n = 5
	end := min(len(contentLines), bestMatchI+len(searchLines)+n)
	start := max(0, bestMatchI-n)
	return strings.Join(contentLines[start:end], "\n")
}

// unindent strips leading whitespace for comparison only.
func unindent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimLeft(l, " \t")
	}
	return out
}

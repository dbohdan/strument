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
func FindSimilarLines(search, content string, threshold float64) string {
	searchLines := splitLinesNoEnds(search)
	contentLines := splitLinesNoEnds(content)

	bestRatio := 0.0
	var bestMatch []string
	bestMatchI := -1

	for i := 0; i+len(searchLines) <= len(contentLines); i++ {
		chunk := contentLines[i : i+len(searchLines)]
		ratio := lineRatio(searchLines, chunk)
		if ratio > bestRatio {
			bestRatio = ratio
			bestMatch = chunk
			bestMatchI = i
		}
	}

	if bestRatio < threshold {
		return ""
	}

	if len(bestMatch) > 0 && len(searchLines) > 0 &&
		bestMatch[0] == searchLines[0] && bestMatch[len(bestMatch)-1] == searchLines[len(searchLines)-1] {
		return strings.Join(bestMatch, "\n")
	}

	const n = 5
	end := min(len(contentLines), bestMatchI+len(searchLines)+n)
	start := max(0, bestMatchI-n)
	return strings.Join(contentLines[start:end], "\n")
}

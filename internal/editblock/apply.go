package editblock

import (
	"fmt"
	"sort"
	"strings"
)

// FileReader supplies current file content for planning. Implementations
// read from disk, an overlay, or a fixture.
type FileReader interface {
	// ReadFile returns the content of the file at rel path, and whether it
	// exists. A read error on an existing file may be reported as
	// exists=false content="" — aider treats unreadable as missing here.
	ReadFile(path string) (content string, exists bool)
}

// PlanResult is the outcome of planning a batch of edits against a file
// state, without touching the filesystem (basecoder-spec §7.1 wants an
// in-memory plan; the coder writes it atomically).
type PlanResult struct {
	// Writes maps each touched path to its final content, and WriteOrder
	// records first-touch order for deterministic application.
	Writes     map[string]string
	WriteOrder []string
	// Applied are the edits that matched (with reattributed paths);
	// Failed are the ones that didn't.
	Applied []Edit
	Failed  []Edit
	// Report is the model-facing failure report (editblock-spec §5);
	// empty when nothing failed.
	Report string
}

// overlay layers planned writes over a FileReader so sequential edits
// against one file compose in order.
type overlay struct {
	base   FileReader
	writes map[string]string
}

func (o *overlay) ReadFile(path string) (string, bool) {
	if c, ok := o.writes[path]; ok {
		return c, true
	}
	return o.base.ReadFile(path)
}

// ApplyEdits ports apply_edits as a pure planner. chatFiles are the in-chat
// editable relative paths used for the cross-file retry; they are iterated
// in sorted order (determinism divergence: aider iterates a Python set).
func ApplyEdits(edits []Edit, chatFiles []string, files FileReader, fence Fence) PlanResult {
	res := PlanResult{Writes: map[string]string{}}
	ov := &overlay{base: files, writes: res.Writes}

	sortedChat := append([]string(nil), chatFiles...)
	sort.Strings(sortedChat)

	for _, edit := range edits {
		path := edit.Path
		var newContent string
		ok := false

		if content, exists := ov.ReadFile(path); exists {
			newContent, ok = DoReplace(path, content, true, edit.Search, edit.Replace, fence)
		} else if strings.TrimSpace(edit.Search) == "" {
			// New file: the coder's prepareToEdit creates it (with
			// confirmation) before aider's apply_edits ever runs; the
			// planner folds that in.
			newContent, ok = DoReplace(path, "", false, edit.Search, edit.Replace, fence)
		}

		// Cross-file retry: models regularly put the right block under the
		// wrong filename.
		if (!ok || newContent == "") && strings.TrimSpace(edit.Search) != "" {
			for _, chatPath := range sortedChat {
				content, exists := ov.ReadFile(chatPath)
				if !exists {
					continue
				}
				if c, cok := DoReplace(chatPath, content, true, edit.Search, edit.Replace, fence); cok && c != "" {
					newContent, ok = c, true
					path = chatPath
					break
				}
			}
		}

		// aider treats an empty result as falsy, i.e. a failure.
		if ok && newContent != "" {
			if _, seen := res.Writes[path]; !seen {
				res.WriteOrder = append(res.WriteOrder, path)
			}
			res.Writes[path] = newContent
			res.Applied = append(res.Applied, Edit{Path: path, Search: edit.Search, Replace: edit.Replace})
		} else {
			res.Failed = append(res.Failed, edit)
		}
	}

	if len(res.Failed) > 0 {
		res.Report = failureReport(res.Failed, len(res.Applied), ov, fence)
	}
	return res
}

// failureReport builds the model-facing report, byte-for-byte with aider
// (editblock-spec §5). File contents are read post-application so
// did-you-mean reflects successful edits to the same file.
func failureReport(failed []Edit, passedCount int, files FileReader, fence Fence) string {
	blocks := "block"
	if len(failed) != 1 {
		blocks = "blocks"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %d SEARCH/REPLACE %s failed to match!\n", len(failed), blocks)
	for _, edit := range failed {
		content, _ := files.ReadFile(edit.Path)

		fmt.Fprintf(&b, `
## SearchReplaceNoExactMatch: This SEARCH block failed to exactly match lines in %s
<<<<<<< SEARCH
%s=======
%s>>>>>>> REPLACE

`, edit.Path, edit.Search, edit.Replace)

		if didYouMean := FindSimilarLines(edit.Search, content, 0.6); didYouMean != "" {
			fmt.Fprintf(&b, `Did you mean to match some of these actual lines from %s?

%s
%s
%s

`, edit.Path, fence.Open, didYouMean, fence.Close)
		}

		if edit.Replace != "" && strings.Contains(content, edit.Replace) {
			fmt.Fprintf(&b, `Are you sure you need this SEARCH/REPLACE block?
The REPLACE lines are already in %s!

`, edit.Path)
		}
	}
	b.WriteString("The SEARCH section must exactly match an existing block of lines including all white space, comments, indentation, docstrings, etc\n")
	if passedCount > 0 {
		pblocks := "block"
		if passedCount != 1 {
			pblocks = "blocks"
		}
		fmt.Fprintf(&b, `
# The other %d SEARCH/REPLACE %s were applied successfully.
Don't re-send them.
Just reply with fixed versions of the %s above that failed to match.
`, passedCount, pblocks, blocks)
	}
	return b.String()
}

// FindSimilarLines ports find_similar_lines: the best window of content
// lines resembling the search lines at ratio >= threshold, expanded by 5
// lines each way unless the endpoints already line up (editblock-spec §5).
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

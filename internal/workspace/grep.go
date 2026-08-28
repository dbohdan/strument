package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// GrepMode selects how much a search returns.
type GrepMode int

const (
	// GrepFiles returns only the paths that contain a match. It is the
	// default because it is the cheapest useful answer: a model orienting
	// itself wants to know where to look before it wants the lines.
	GrepFiles GrepMode = iota
	// GrepContent returns the matching lines with their line numbers.
	GrepContent
	// GrepCount returns a per-file match count.
	GrepCount
)

// GrepQuery is one content search.
type GrepQuery struct {
	// Pattern is a Go regular expression.
	Pattern string
	// Glob, when set, restricts the search to matching paths (** supported).
	Glob string
	// Dir, when set, restricts the search to a subtree.
	Dir string
	// IgnoreCase folds case.
	IgnoreCase bool
	Mode       GrepMode
	// ContextLines is how many lines to return either side of a match, like
	// grep's -C. It applies to GrepContent only; the caller implies that mode
	// when it asks for context, since context around a file *list* is not a
	// thing anyone wants.
	ContextLines int
}

// GrepFileResult is one file's matches.
type GrepFileResult struct {
	Path  string
	Count int
	// Lines is populated in GrepContent mode only.
	Lines []GrepLine
}

// GrepLine is one line of a content result: a match, or a line of context
// around one.
type GrepLine struct {
	Number int // 1-based
	Text   string
	// Match distinguishes a line the pattern hit from one that came along for
	// context. Without it a caller cannot render the difference, and a model
	// reading twenty lines cannot tell which one it searched for — grep's own
	// output solves this with ":" against "-", and so does the renderer.
	Match bool
	// GapBefore marks a line that does not follow the previous one in this
	// file, so a reader is not left to infer a jump from the numbers.
	GapBefore bool
}

// GrepResult is a whole search.
type GrepResult struct {
	Files     []GrepFileResult
	Total     int // matching lines across all files
	Truncated Truncated
	// InScope counts the files Glob and Dir admitted, and Scanned the subset
	// actually searched — the rest were binary or over the size cap.
	//
	// They exist so a caller can tell three different nothings apart: a scope
	// that admitted no files (the pattern was never tested), files that could
	// not be read, and a pattern that genuinely is not there. Reporting all
	// three as "no matches" tells a reader the identifier does not exist in the
	// project, which is a different claim and often a false one.
	InScope int
	Scanned int
	// Shortened counts returned lines that were clipped to MaxMatchBytes, so
	// the caller can say so rather than let a "…" pass for the file's content.
	Shortened int
}

// clipRunes cuts s to at most n bytes without splitting a rune, marking the cut
// so a clipped line cannot be mistaken for the whole one. The marker is the
// same "…" the diff renderer elides with.
func clipRunes(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " …", true
}

// Grep searches file contents across the workspace.
//
// This is a structured search rather than a shell-out to grep or rg. Doing it
// in process is what lets the result be capped, keeps the behavior identical
// on Windows, and means the search obeys the same ignore rules as everything
// else here. A model left to run `rg -n foo | head -50` through the shell would
// take the cap out of the harness's hands, and would need the shell gate for
// what is a pure observation.
func (w *Workspace) Grep(q GrepQuery) (GrepResult, error) {
	expr := q.Pattern
	if q.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return GrepResult{}, err
	}

	dir := path(q.Dir)
	glob := path(q.Glob)

	var res GrepResult
	emitted := 0
	capped := false
	trunc, err := w.walk(func(rel string, d fs.DirEntry) bool {
		if d.IsDir() {
			return true
		}
		if dir != "" && !strings.HasPrefix(rel, dir+"/") && rel != dir {
			return true
		}
		if glob != "" && !matchGlob(glob, rel) {
			return true
		}
		res.InScope++

		if info, err := d.Info(); err == nil && info.Size() > w.Limits.fileBytes() {
			return true
		}
		data, err := os.ReadFile(filepath.Join(w.Root, filepath.FromSlash(rel)))
		if err != nil || isBinary(data) {
			return true
		}
		res.Scanned++

		fileRes := GrepFileResult{Path: rel}
		lines := splitLines(string(data))

		// Which lines matched, before any are emitted. Marking them during the
		// walk gets it wrong whenever one match falls inside the window of an
		// earlier one: the line is already on screen as context and never
		// re-marked, so a hit disappears from a listing that counted it. Found
		// by running the thing, not by reading it.
		isMatch := make([]bool, len(lines))
		var matched []int
		for i, line := range lines {
			if re.MatchString(line) {
				isMatch[i] = true
				matched = append(matched, i)
			}
		}
		fileRes.Count = len(matched)
		res.Total += len(matched)

		if q.Mode == GrepContent {
			lastEmitted := -1
			for _, i := range matched {
				// The window this match wants, clamped to the file and to
				// whatever an earlier match already emitted, so overlapping
				// context merges into one block instead of repeating.
				lo := max(i-q.ContextLines, 0)
				hi := min(i+q.ContextLines, len(lines)-1)
				if lo <= lastEmitted {
					lo = lastEmitted + 1
				}
				if lo > hi {
					continue // wholly inside a block already emitted
				}
				gap := lastEmitted >= 0 && lo > lastEmitted+1
				for n := lo; n <= hi; n++ {
					text, clipped := clipRunes(lines[n], w.Limits.matchBytes())
					if clipped {
						res.Shortened++
					}
					fileRes.Lines = append(fileRes.Lines, GrepLine{
						Number: n + 1, Text: text, Match: isMatch[n], GapBefore: gap && n == lo,
					})
					emitted++
				}
				lastEmitted = hi
				// The cap counts emitted lines rather than matches, because
				// that is what reaches the context: at five lines of context a
				// hundred matches would otherwise return eleven hundred lines.
				if emitted >= w.Limits.matches() {
					capped = true
					break
				}
			}
		}
		if fileRes.Count > 0 {
			res.Files = append(res.Files, fileRes)
		}

		// In content mode the cap counts emitted lines, since that is what
		// reaches the context; otherwise it counts paths.
		if q.Mode == GrepContent {
			return !capped
		}
		return len(res.Files) < w.Limits.results()
	})
	if err != nil {
		return res, err
	}

	res.Truncated = trunc
	if q.Mode == GrepContent && capped {
		res.Truncated.Results = true
	}
	if q.Mode != GrepContent && len(res.Files) >= w.Limits.results() {
		res.Truncated.Results = true
	}

	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	return res, nil
}

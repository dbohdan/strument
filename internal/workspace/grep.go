package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
}

// GrepFileResult is one file's matches.
type GrepFileResult struct {
	Path  string
	Count int
	// Lines is populated in GrepContent mode only.
	Lines []GrepLine
}

// GrepLine is one matching line.
type GrepLine struct {
	Number int // 1-based
	Text   string
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
		for i, line := range splitLines(string(data)) {
			if !re.MatchString(line) {
				continue
			}
			fileRes.Count++
			res.Total++
			if q.Mode == GrepContent {
				fileRes.Lines = append(fileRes.Lines, GrepLine{Number: i + 1, Text: line})
			}
		}
		if fileRes.Count > 0 {
			res.Files = append(res.Files, fileRes)
		}

		// In content mode the cap counts lines, since that is what reaches the
		// context; otherwise it counts files.
		if q.Mode == GrepContent {
			return res.Total < w.Limits.results()
		}
		return len(res.Files) < w.Limits.results()
	})
	if err != nil {
		return res, err
	}

	res.Truncated = trunc
	if q.Mode == GrepContent && res.Total >= w.Limits.results() {
		res.Truncated.Results = true
	}
	if q.Mode != GrepContent && len(res.Files) >= w.Limits.results() {
		res.Truncated.Results = true
	}

	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	return res, nil
}

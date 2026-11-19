package workspace

import (
	pathpkg "path"
	"slices"
	"strings"

	"github.com/agnivade/levenshtein"
	"golang.org/x/text/unicode/norm"
)

// maxSuggestions is how many near names a miss offers.
const maxSuggestions = 5

// Suggest answers a path that names nothing. same is the one file whose name
// differs from rel's only in how its characters are written — composed or
// decomposed accents, a narrow or no-break space for a space, a curly quote
// for a straight one — or "" when there is not exactly one. A model cannot
// reliably type U+202F, which macOS puts before "PM" in a screenshot's name,
// so offering that name back as a suggestion would fail the same way.
//
// near is up to maxSuggestions other paths the model may have meant: names in
// the same directory within two edits of rel's, case aside (AGENT.md for
// AGENTS.md), then files of the same name elsewhere in the project, for a
// directory guessed wrong. Both come through List and Files, so the ignore
// rules and the containment apply as they do to any listing.
func (w *Workspace) Suggest(rel string) (same string, near []string) {
	rel = strings.TrimSuffix(strings.ReplaceAll(rel, "\\", "/"), "/")
	dir, base := pathpkg.Dir(rel), pathpkg.Base(rel)
	if dir == "." {
		dir = ""
	}
	type cand struct {
		p    string
		dist int
	}
	var cands []cand
	seen := map[string]bool{}
	add := func(p string, d int) {
		if !seen[p] && p != rel {
			seen[p] = true
			cands = append(cands, cand{p, d})
		}
	}

	if l, err := w.List(dir); err == nil {
		var spelled []string
		key, lower := spelling(base), strings.ToLower(base)
		for _, e := range l.Entries {
			name := pathpkg.Base(e.Path)
			if spelling(name) == key {
				spelled = append(spelled, e.Path)
				continue
			}
			// Two edits on a three-letter name reaches nearly anything.
			limit := 2
			if len([]rune(base)) <= 4 {
				limit = 1
			}
			if d := levenshtein.ComputeDistance(lower, strings.ToLower(name)); d <= limit {
				add(e.Path, d)
			}
		}
		if len(spelled) == 1 {
			return spelled[0], nil
		}
		for _, p := range spelled {
			add(p, 0)
		}
	}
	if files, _, err := w.Files(); err == nil {
		for _, f := range files {
			if strings.EqualFold(pathpkg.Base(f), base) {
				add(f, 3)
			}
		}
	}
	slices.SortStableFunc(cands, func(a, b cand) int { return a.dist - b.dist })
	for _, c := range cands[:min(len(cands), maxSuggestions)] {
		near = append(near, c.p)
	}
	return "", near
}

// spelling is a name with the differences a person cannot see taken out.
func spelling(name string) string {
	return spellingReplacer.Replace(norm.NFC.String(name))
}

var spellingReplacer = strings.NewReplacer(
	" ", " ", " ", " ", " ", " ",
	"‘", "'", "’", "'", "“", `"`, "”", `"`,
)

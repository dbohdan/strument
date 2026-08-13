package repl

import (
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"

	"dbohdan.com/strument/internal/readline"
)

// promptCompleter routes Tab completion: a line that starts with "/" goes to the
// slash-command completer (cmd, the existing PrefixCompleter tree), and any other
// line completes the file-path word under the cursor against the current
// chat/repo files. This adds aider-style file completion in the main prompt
// without disturbing command completion.
type promptCompleter struct {
	cmd   readline.AutoCompleter
	files func() []string
}

var _ readline.AutoCompleter = promptCompleter{}

func (p promptCompleter) Do(line []rune, pos int) ([][]rune, int) {
	if len(line) > 0 && line[0] == '/' {
		return p.cmd.Do(line, pos)
	}
	return completeWord(line, pos, p.files())
}

// completeWord completes the whitespace-delimited token ending at pos against
// candidates, returning each match's suffix past the token and the token length
// — the readline.AutoCompleter contract (suffixes, sharedLen). Matching is a
// case-sensitive rune prefix: the typed prefix stays in the buffer, so folding
// case would corrupt it. An empty token completes nothing, since it would
// otherwise offer every file.
func completeWord(line []rune, pos int, candidates []string) ([][]rune, int) {
	if pos < 0 || pos > len(line) {
		return nil, 0
	}
	start := pos
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	token := line[start:pos]
	if len(token) == 0 {
		return nil, 0
	}
	seen := map[string]struct{}{}
	var out [][]rune
	for _, c := range candidates {
		cr := []rune(c)
		if len(cr) <= len(token) || !runesHasPrefix(cr, token) {
			continue
		}
		suffix := string(cr[len(token):])
		if _, dup := seen[suffix]; dup {
			continue
		}
		seen[suffix] = struct{}{}
		out = append(out, []rune(suffix))
	}
	slices.SortFunc(out, func(a, b []rune) int { return strings.Compare(string(a), string(b)) })
	return out, len(token)
}

func runesHasPrefix(s, prefix []rune) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

// completePromptFiles is the candidate set for main-prompt completion, fetched
// fresh each Tab so it tracks /add, /drop, and newly created files. It unions
// the tracked, chat, and read-only files, each contributing both its
// repo-relative path and its basename (so "scra" completes "scrape.go" and
// "internal/co" descends the path). Without git, TrackedFiles is nil, so
// completion falls back to this session's editable and reference files.
func (r *REPL) completePromptFiles() []string {
	set := map[string]struct{}{}
	add := func(rel string) {
		if rel == "" {
			return
		}
		set[rel] = struct{}{}
		if b := path.Base(rel); b != rel {
			set[b] = struct{}{}
		}
	}
	for _, f := range r.coder.TrackedFiles() {
		add(f)
	}
	for _, f := range r.coder.ChatFiles() {
		add(f)
	}
	for _, f := range r.coder.ReadOnlyFiles() {
		add(f)
	}
	return slices.Sorted(maps.Keys(set))
}

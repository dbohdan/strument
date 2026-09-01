package repl

import (
	"maps"
	"os"
	"path"
	"path/filepath"
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

// completePaths completes /read-only and /submit arguments against the real
// filesystem, absolute paths included. completeAddable cannot serve these
// commands: it lists one level of the project root as root-relative names, so
// /tmp/... could never appear — yet pinning outside-root material is /read-only's
// documented purpose, and /submit's example use is a draft prompt in /tmp.
//
// The dynamic callback receives the whole typed line, so the word being
// completed is extracted here. Candidates are returned in the same terms the
// word was typed in, because the prefix completer matches the remaining line
// — the typed word, directory parts included — against each candidate and
// offers the suffix past it: `internal/co` is answered with `internal/…`
// candidates, `/tmp/…/pro` with absolute ones. A relative word's directory
// resolves against the project root, the same resolution cmdSubmit and
// expandPatterns apply. A directory entry carries a trailing slash, which
// both signals "descend" and lets the next Tab list into it. Dotfiles stay
// hidden unless the typed word starts with one, as a shell does.
func (r *REPL) completePaths(line string) []string {
	raw := lastCommandWord(line)
	lookup := unescapeWord(raw)
	absolute := filepath.IsAbs(lookup)
	if !absolute {
		lookup = filepath.Join(r.coder.Root, lookup)
	}
	dir, base := filepath.Split(lookup)
	if dir == "" {
		dir = "./"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // a directory that does not exist completes to nothing
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		full := dir + name
		if e.IsDir() {
			full += "/"
		}
		// Re-express the entry in the terms the word was typed in — absolute
		// for an absolute word, root-relative otherwise — because the prefix
		// completer matches the remaining line (the whole word, directory
		// parts included) against each candidate and offers the suffix past
		// it: the candidate must begin with what the user typed.
		cand := escapePathWord(full)
		if !absolute {
			rel, err := filepath.Rel(r.coder.Root, full)
			if err != nil {
				continue
			}
			cand = escapePathWord(rel)
		}
		out = append(out, cand)
	}
	slices.Sort(out)
	return out
}

// lastCommandWord extracts the raw argument word being completed from a
// slash-command line: the text from the start of its last field. It follows
// splitArgs' tokenizer only far enough to find where that field begins — a
// space inside quotes or after a backslash does not end it — because the
// prefix completer matches against the raw buffer: the typed prefix stays in
// the buffer, so what Tab inserts is appended to exactly this text. Quotes
// and escapes stay in the returned word; the complement is unescapeWord for
// the filesystem lookup and escapePathWord for the offered text.
func lastCommandWord(line string) string {
	esc := backslashEscapes
	rs := []rune(line)
	fieldStart := 0
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\\':
			if esc {
				i++ // the escaped rune cannot end a field or open a quote
			}
		case '\'':
			i++
			for i < len(rs) && rs[i] != '\'' {
				i++
			}
		case '"':
			i++
			for i < len(rs) && rs[i] != '"' {
				if rs[i] == '\\' && esc && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
					i++
				}
				i++
			}
		case ' ', '\t':
			fieldStart = i + 1
		}
	}
	if fieldStart > len(rs) {
		return ""
	}
	return string(rs[fieldStart:])
}

// unescapeWord removes the quoting splitArgs will apply to a word: backslash
// escapes where the platform says they apply, then a surrounding quote pair.
// This is the path actually looked up on disk; the offered completion is
// re-escaped by escapePathWord so the buffer stays parseable.
func unescapeWord(w string) string {
	if backslashEscapes {
		var b strings.Builder
		rs := []rune(w)
		for i := 0; i < len(rs); i++ {
			if rs[i] == '\\' && i+1 < len(rs) {
				i++
			}
			b.WriteRune(rs[i])
		}
		w = b.String()
	}
	return strings.Trim(w, `"'`)
}

// escapePathWord escapes spaces (and backslashes, first) so what Tab inserts
// re-parses, via splitArgs, to the same path — on platforms where a backslash
// escapes. On Windows a backslash is a path separator, so a path with spaces
// must be quoted by hand instead: the same trade-off splitArgs documents.
func escapePathWord(p string) string {
	if !backslashEscapes {
		return p
	}
	p = strings.ReplaceAll(p, `\`, `\\`)
	return strings.ReplaceAll(p, " ", `\ `)
}

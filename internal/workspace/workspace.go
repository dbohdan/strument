// Package workspace is the file-access layer behind the read, ls, glob, and
// grep tools: one way to see the project, whether or not it is a git
// repository.
//
// It deliberately does not ask git which files exist. Shelling out to
// `git ls-files` would give a different answer with and without a repository —
// and no answer at all in a plain directory, which is a case Strument
// supports (editing live configuration, or a project under another SCM). So the
// tree is walked directly and .gitignore rules are applied in process, through
// the vendored matcher in internal/gitignore. A new file the model just wrote
// is visible immediately, which `git ls-files` would not show until it was
// staged.
package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"dbohdan.com/strument/internal/gitignore"
)

// Limits bound what a single call may traverse or return, so one tool call
// cannot stall the turn or flood the context. Zero means the default.
// A path and a matching line are different currencies, which is why they have
// separate caps. Measured on this repo: a path averages 37 bytes, so even a
// thousand of them is about 9k tokens and the ceiling is predictable. A
// *matching line* is whatever happened to be on that line — the median for one
// unscoped search here was 1383 bytes and the longest was 157 KB, a single
// line in a recorded fixture. Capping the two with one number meant the cheap
// case was throttled and the expensive one was not bounded at all.
type Limits struct {
	// MaxEntries caps how many filesystem entries a walk visits.
	MaxEntries int
	// MaxResults caps how many paths a call returns: ls, glob, and grep in its
	// files and count modes.
	MaxResults int
	// MaxMatches caps how many matching lines a content search returns.
	MaxMatches int
	// MaxMatchBytes caps the length of one returned matching line.
	MaxMatchBytes int
	// MaxFileBytes caps the size of a file that will be read or searched.
	MaxFileBytes int64
}

const (
	defaultMaxEntries = 200_000
	defaultMaxResults = 1_000
	// A content search is for finding a definition or a call site, and past a
	// hundred lines the pattern is wrong rather than the limit. The cost of
	// stopping early is one more call; the cost of not stopping is tens of
	// thousands of tokens that every later step in the turn pays for again.
	defaultMaxMatches = 100
	// Enough for any line of source — the median match in this project's own Go
	// files is 58 bytes and the 90th percentile is 81 — and short enough that a
	// minified blob or a recorded JSON fixture cannot bring its whole line.
	defaultMaxMatchBytes = 200
	defaultMaxFileBytes  = 5 << 20 // 5 MiB
)

func (l Limits) entries() int {
	if l.MaxEntries > 0 {
		return l.MaxEntries
	}
	return defaultMaxEntries
}

func (l Limits) results() int {
	if l.MaxResults > 0 {
		return l.MaxResults
	}
	return defaultMaxResults
}

func (l Limits) matches() int {
	if l.MaxMatches > 0 {
		return l.MaxMatches
	}
	return defaultMaxMatches
}

func (l Limits) matchBytes() int {
	if l.MaxMatchBytes > 0 {
		return l.MaxMatchBytes
	}
	return defaultMaxMatchBytes
}

func (l Limits) fileBytes() int64 {
	if l.MaxFileBytes > 0 {
		return l.MaxFileBytes
	}
	return defaultMaxFileBytes
}

// Workspace reads one project tree rooted at Root.
type Workspace struct {
	Root   string
	Limits Limits
	// Pinned reports whether an absolute path is one the user explicitly
	// pinned with /add or /read-only. Those are sanctioned wherever they live,
	// the same exemption unsafePath makes for edits: containment guards against
	// a model inventing a path out of the project, not against what the user
	// deliberately reached for.
	//
	// A predicate rather than a list, because a list would go stale the first
	// time /drop ran. nil means nothing is pinned, which is what `strument
	// tool` passes: the command line is contained with no exception at all.
	Pinned func(abs string) bool
}

// skipAlways is the only unconditional exclusion: the repository's own
// internals, which are machine state rather than project content.
//
// Dot-files are deliberately NOT hidden. In a project tree they are ordinary
// editable files — .gitignore, .golangci.toml, .github/workflows — and a
// harness that cannot see them cannot edit its own CI config. What should stay
// out of view is what the project already declares out of view, and .gitignore
// says that far better than a leading dot does.
const skipAlways = ".git"

// New builds a Workspace over root with the default limits.
func New(root string) *Workspace { return &Workspace{Root: root} }

// Entry is one directory entry, as List reports it.
type Entry struct {
	// Path is root-relative and forward-slashed.
	Path  string
	IsDir bool
	Size  int64
	// Link is a symlink's target as written, empty for everything else. A
	// listing that doesn't say so shows one file under two names and gives the
	// reader no way to tell it from a duplicate.
	Link string
}

// Truncated reports that a result was cut short by a limit. Callers surface
// this to the model, because a silently short answer reads as "nothing more
// exists" and sends it down the wrong path.
type Truncated struct {
	Entries bool // the walk hit MaxEntries
	Results bool // the result hit MaxResults
}

func (t Truncated) Any() bool { return t.Entries || t.Results }

// walker carries one traversal. The entry budget lives here so it is shared
// across the whole walk, while the ignore patterns are passed down by value:
// they accumulate along a branch and must not leak into a sibling.
type walker struct {
	w       *Workspace
	fn      func(rel string, d fs.DirEntry) bool
	visited int
	stopped bool // the entry budget ran out
	done    bool // fn asked to stop; unwinds the whole walk, not just one level
}

// walk descends from the workspace root, calling fn for every file and
// directory that survives the ignore rules. fn returns false to stop early.
// Directories are reported before their contents, and an ignored directory is
// pruned rather than descended — the same shortcut git takes, and the reason a
// node_modules tree costs nothing here.
func (w *Workspace) walk(fn func(rel string, d fs.DirEntry) bool) (Truncated, error) {
	root, err := gitignore.ReadRoot(w.Root)
	if err != nil {
		return Truncated{}, err
	}
	wk := &walker{w: w, fn: fn}
	if err := wk.dir(w.Root, nil, root); err != nil {
		return Truncated{}, err
	}
	return Truncated{Entries: wk.stopped}, nil
}

// dir walks one directory. domain is its path components relative to the root;
// patterns are the ignore rules in scope, in ascending priority.
func (wk *walker) dir(dir string, domain []string, patterns []gitignore.Pattern) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// An unreadable directory is not fatal: a walk over someone's project
		// should report what it can rather than fail wholesale.
		return nil //nolint:nilerr // Deliberate: skip what we cannot read.
	}

	// A nested .gitignore applies to this directory and below, at higher
	// priority than everything inherited. Concat, never append: appending to
	// the inherited slice could share a backing array with a sibling branch and
	// leak this directory's rules into it.
	if len(domain) > 0 {
		own, err := gitignore.ReadDir(dir, domain)
		if err != nil {
			return err
		}
		if len(own) > 0 {
			patterns = slices.Concat(patterns, own)
		}
	}
	matcher := gitignore.NewMatcher(patterns)

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if wk.done || wk.stopped {
			return nil
		}

		name := e.Name()
		if name == skipAlways {
			continue
		}

		wk.visited++
		if wk.visited > wk.w.Limits.entries() {
			wk.stopped = true
			return nil
		}

		components := append(slices.Clone(domain), name)
		isDir := e.IsDir()
		if matcher.Match(components, isDir) {
			continue
		}

		if !wk.fn(strings.Join(components, "/"), e) {
			wk.done = true
			return nil
		}

		if isDir {
			if err := wk.dir(filepath.Join(dir, name), components, patterns); err != nil {
				return err
			}
		}
	}
	return nil
}

// Files lists every non-ignored file in the tree, root-relative, sorted.
func (w *Workspace) Files() ([]string, Truncated, error) {
	var out []string
	trunc, err := w.walk(func(rel string, d fs.DirEntry) bool {
		if d.IsDir() {
			return true
		}
		out = append(out, rel)
		return len(out) < w.Limits.results()
	})
	if err != nil {
		return nil, trunc, err
	}
	if len(out) >= w.Limits.results() {
		trunc.Results = true
	}
	return out, trunc, nil
}

// List reports the immediate contents of one directory, root-relative and
// sorted, directories included. dir is root-relative; "" or "." is the root.
//
// It is not redundant with Glob: models use ls to orient themselves in an
// unfamiliar tree, and answer "what is in here" badly when they have to guess
// a pattern first.
func (w *Workspace) List(dir string) ([]Entry, error) {
	full, rel, reason := w.contain(dir)
	if reason != "" {
		return nil, errors.New(reason)
	}
	var domain []string
	if rel != "" {
		domain = strings.Split(rel, "/")
	}

	matcher, err := w.matcherFor(domain)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == skipAlways {
			continue
		}
		components := append(slices.Clone(domain), name)
		if matcher.Match(components, e.IsDir()) {
			continue
		}
		ent := Entry{Path: strings.Join(components, "/"), IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil && !e.IsDir() {
			ent.Size = info.Size()
			if info.Mode()&os.ModeSymlink != 0 {
				ent.Link, _ = os.Readlink(filepath.Join(full, name))
			}
		}
		out = append(out, ent)
		if len(out) >= w.Limits.results() {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Glob returns the non-ignored files matching pattern, root-relative and
// sorted. The pattern is slash-separated and supports ** for "zero or more
// path segments", which is what models reach for and what path.Match alone
// does not provide.
func (w *Workspace) Glob(pattern string) ([]string, Truncated, error) {
	pattern = path(pattern)
	var out []string
	trunc, err := w.walk(func(rel string, d fs.DirEntry) bool {
		if d.IsDir() {
			return true
		}
		if matchGlob(pattern, rel) {
			out = append(out, rel)
		}
		return len(out) < w.Limits.results()
	})
	if err != nil {
		return nil, trunc, err
	}
	if len(out) >= w.Limits.results() {
		trunc.Results = true
	}
	sort.Strings(out)
	return out, trunc, nil
}

// path normalizes a caller-supplied path to the forward-slashed, unrooted form
// the walker produces.
func path(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// matchGlob reports whether a slash-separated path matches a glob pattern,
// segment by segment. "**" matches zero or more segments; every other segment
// is matched with path.Match semantics, so a single "*" never crosses a
// separator.
func matchGlob(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Trailing ** matches whatever is left, including nothing.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], seg[0])
		if err != nil || !ok {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

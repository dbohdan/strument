package gitignore

import (
	"os"
	"path/filepath"
	"testing"
)

// write creates path under dir, making parents as needed.
func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadRoot covers the two root-level sources and their priority order:
// .git/info/exclude first, the root .gitignore after it, so the latter wins.
// It also pins the comment and blank-line filtering, which lives in this
// package rather than in ParsePattern.
func TestReadRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".git/info/exclude", "# a comment\n\nsecret.txt\n")
	write(t, dir, ".gitignore", "*.log\n!keep.log\n")

	ps, err := ReadRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMatcher(ps)

	for _, tc := range []struct {
		name string
		path []string
		want bool
	}{
		{"from .git/info/exclude", []string{"secret.txt"}, true},
		{"from .gitignore", []string{"app.log"}, true},
		{"negated by a later pattern", []string{"keep.log"}, false},
		{"unmatched", []string{"main.go"}, false},
		{"a bare pattern applies at any depth", []string{"sub", "app.log"}, true},
	} {
		if got := m.Match(tc.path, false); got != tc.want {
			t.Errorf("%s: Match(%v) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

// TestReadDirDomain confirms a nested .gitignore is scoped to its directory:
// its patterns must not reach siblings.
func TestReadDirDomain(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "vendor/.gitignore", "*.go\n")

	ps, err := ReadDir(filepath.Join(dir, "vendor"), []string{"vendor"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMatcher(ps)

	if !m.Match([]string{"vendor", "lib.go"}, false) {
		t.Error("vendor/lib.go should be ignored by vendor/.gitignore")
	}
	if m.Match([]string{"internal", "lib.go"}, false) {
		t.Error("internal/lib.go must not be ignored by vendor/.gitignore")
	}
}

// TestMissingFileIsNotAnError pins the contract the walker relies on: most
// directories have no .gitignore, and that is not a failure.
func TestMissingFileIsNotAnError(t *testing.T) {
	ps, err := ReadDir(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("missing .gitignore returned %v, want nil", err)
	}
	if len(ps) != 0 {
		t.Errorf("patterns = %d, want 0", len(ps))
	}
}

// TestDirOnlyPattern covers the trailing-slash rule, which decides whether a
// walk may prune a whole subtree.
func TestDirOnlyPattern(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "build/\n")

	ps, err := ReadRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMatcher(ps)

	if !m.Match([]string{"build"}, true) {
		t.Error("the build directory should be ignored")
	}
	if m.Match([]string{"build"}, false) {
		t.Error("a regular file named build must not match a directory-only pattern")
	}
	if !m.Match([]string{"build", "out.o"}, false) {
		t.Error("files under an ignored directory should be ignored")
	}
}

// TestWildmatchDoubleStar exercises the vendored matcher's ** handling, the
// main reason this package tracks go-git's newer wildmatch port rather than
// the older, simpler implementation.
func TestWildmatchDoubleStar(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "a/**/b\n**/node_modules\ndoc/*.html\n")

	ps, err := ReadRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMatcher(ps)

	for _, tc := range []struct {
		path []string
		want bool
	}{
		{[]string{"a", "b"}, true},
		{[]string{"a", "x", "b"}, true},
		{[]string{"a", "x", "y", "b"}, true},
		{[]string{"src", "node_modules"}, true},
		{[]string{"doc", "git.html"}, true},
		// A single * must not cross a separator (fnmatch FNM_PATHNAME).
		{[]string{"doc", "ppc", "ppc.html"}, false},
	} {
		if got := m.Match(tc.path, false); got != tc.want {
			t.Errorf("Match(%v) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// The repository's own .git, refused on every path and however it is spelled.

package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// The spellings that matter, and the ones that must keep working.
//
// The case variants are not hypothetical: .GIT/config opens the real file on
// APFS and NTFS, so an exact-match guard is a live hole on two of the three
// platforms Strument supports. The negatives are the half that keeps this
// honest — .github and .gitignore are ordinary editable project files, and a
// harness that cannot edit its own CI config is worse than one that can.
func TestUnderGitDir(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{".git", true},
		{".git/config", true},
		{".git/hooks/pre-commit", true},
		{".GIT/config", true},
		{".Git/config", true},
		{".git./config", true}, // Win32 strips the trailing dot
		{".git /config", true}, // and the trailing space
		{`.git\config`, true},  // backslash-separated, on every platform
		{"vendor/dep/.git", true},
		{"a/b/.git/objects/x", true},

		{"", false},
		{".github/workflows/ci.yml", false},
		{".gitignore", false},
		{".gitmodules", false},
		{"git/config", false},
		{"src/.gitkeep", false},
		{"notgit/.gitattributes", false},
	} {
		if got := UnderGitDir(tc.path); got != tc.want {
			t.Errorf("UnderGitDir(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// The read path refuses it, and refuses it even for a pinned file.
//
// Pinning is the strongest signal the interface has, and it still does not
// unlock this one; it must stay a record of user intent (/add, /read-only).
func TestContainRefusesGitDir(t *testing.T) {
	w := New(t.TempDir())
	w.Pinned = func(string) bool { return true } // everything pinned

	for _, p := range []string{".git/config", ".GIT/config", "/abs/repo/.git/config"} {
		_, _, reason := w.contain(p)
		if reason == "" {
			t.Errorf("contain(%q) allowed it even though it is inside .git", p)
		}
	}
	// And the guard did not swallow the ordinary dot-files beside it.
	if _, _, reason := w.contain(".github/workflows/ci.yml"); reason != "" {
		t.Errorf("contain refused an ordinary dot-file: %s", reason)
	}
}

// EscapesRoot is exact, which is the whole reason it exists as a function.
//
// The idiom it replaces — HasPrefix(rel, "..") — also fires on a file honestly
// named "..notes", and three places in the REPL were doing that. All three
// erred toward refusing, so nothing was ever unsafe; a user with such a file
// was just told it was outside their own project.
func TestEscapesRoot(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"..", true},
		{"../outside", true},
		{"../../etc/passwd", true},

		{"", false},
		{".", false},
		{"a/b.go", false},
		{"..notes", false},    // a real file, not an escape
		{"..hidden/x", false}, // and a real directory
		{"a/../b.go", false},  // Rel never returns this, but it does not escape
	} {
		if got := EscapesRoot(filepath.FromSlash(tc.rel)); got != tc.want {
			t.Errorf("EscapesRoot(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// ResolveSymlinks resolves through the part of a path that exists and keeps the
// rest, which is what makes it usable on a file that is about to be created.
func TestResolveSymlinksKeepsTheMissingTail(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := ResolveSymlinks(filepath.Join(link, "newdir", "new.go"))
	want := filepath.Join(ResolveSymlinks(target), "newdir", "new.go")
	if got != want {
		t.Errorf("ResolveSymlinks through a symlink to a missing file:\n got %s\nwant %s", got, want)
	}
}

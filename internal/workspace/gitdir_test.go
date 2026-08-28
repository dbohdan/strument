// The repository's own .git, refused on every path and however it is spelled.

package workspace

import "testing"

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
// unlock this one: absFnames is appended to by the edit path itself, so a list
// the model can grow must not be able to open the door.
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

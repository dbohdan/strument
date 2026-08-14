package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadAndListAreContained mirrors coder's unsafepath_test.go for the read
// side, which had no equivalent and therefore no check at all. A security
// review confirmed all three against the built binary:
//
//	read ../outside.env      → SECRET=outside
//	read etc-link/hostname   → /etc/hostname, through a symlinked directory
//	ls ..                    → the parent directory
//
// glob and grep were never affected because they walk the tree instead of
// joining a path, which is exactly why this survived: three of the four tools
// behaved, so the fourth looked like it did too.
func TestReadAndListAreContained(t *testing.T) {
	root := tree(t, map[string]string{
		"normal.txt":     "inside\n",
		"sub/nested.txt": "also inside\n",
	})
	outside := filepath.Join(filepath.Dir(root), "outside.env")
	if err := os.WriteFile(outside, []byte("SECRET=outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(root), filepath.Join(root, "up-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	w := New(root)

	for _, tc := range []struct{ name, path, wantErr string }{
		{"parent traversal", "../outside.env", "outside the project root"},
		{"deep traversal", "../../etc/passwd", "outside the project root"},
		{"absolute path", "/etc/passwd", "absolute paths are not allowed"},
		{"through a symlinked directory", "up-link/outside.env", "through a symlink"},
		{"the repository's internals", ".git/config", ".git directory"},
		{"nested .git", "sub/.git/config", ".git directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := w.Read(tc.path, 0, 0); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Read(%q) err = %v, want one mentioning %q", tc.path, err, tc.wantErr)
			}
			// ls takes the same paths and needs the same answer.
			if _, err := w.List(tc.path); err == nil {
				t.Errorf("List(%q) should be refused too", tc.path)
			}
		})
	}

	// A symlink that stays inside the project is ordinary structure — a
	// monorepo alias, a vendored path — and refusing it would be the
	// false positive that matters. The live trial did not cover this: no model
	// in eighteen sessions happened to walk through one, which is exactly why
	// it needs a test rather than a session.
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "in-link")); err == nil {
		if _, err := w.Read("in-link/nested.txt", 0, 0); err != nil {
			t.Errorf("a symlink pointing inside the project must still work: %v", err)
		}
		if _, err := w.List("in-link"); err != nil {
			t.Errorf("List through an inside symlink: %v", err)
		}
	}

	// Ordinary paths are untouched: containment must not become a tax on the
	// common case.
	for _, ok := range []string{"normal.txt", "./normal.txt", "sub/nested.txt"} {
		if _, err := w.Read(ok, 0, 0); err != nil {
			t.Errorf("Read(%q) = %v, want it to work", ok, err)
		}
	}
	if _, err := w.List("sub"); err != nil {
		t.Errorf("List(\"sub\") = %v", err)
	}
}

// A path the user pinned is sanctioned wherever it lives, the same carve-out
// unsafePath makes for edits. /read-only is the only channel for a file outside
// the project, and models with such a file's contents in context were observed
// reading it to check — so containing it without this would break a path the
// design depends on.
func TestPinnedFilesAreExemptFromContainment(t *testing.T) {
	root := tree(t, map[string]string{"normal.txt": "inside\n"})
	outside := filepath.Join(filepath.Dir(root), "spec.md")
	if err := os.WriteFile(outside, []byte("GET /widgets\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := New(root)

	rel := "../" + filepath.Base(outside)
	if _, err := w.Read(rel, 0, 0); err == nil {
		t.Fatal("with nothing pinned, an outside file must be refused")
	}

	w.Pinned = func(abs string) bool { return abs == outside }
	got, err := w.Read(rel, 0, 0)
	if err != nil {
		t.Fatalf("a pinned file should stay readable: %v", err)
	}
	if len(got.Lines) == 0 || !strings.Contains(got.Lines[0], "GET /widgets") {
		t.Errorf("contents = %+v", got.Lines)
	}
	// The exemption is exactly that one file, not its directory.
	if _, err := w.Read("../elsewhere.txt", 0, 0); err == nil {
		t.Error("the exemption must not widen to the whole parent directory")
	}
}

// README.md said the lookup tools "never see a file the project ignores", which
// was true of ls, glob, and grep and false of read: a gitignored .env was
// invisible to every way of finding it and one guessed filename away from being
// read.
func TestReadHonorsGitignore(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":                "secret.env\nnode_modules/\n",
		"secret.env":                "TOKEN=xyz\n",
		"node_modules/pkg/index.js": "module.exports = 1\n",
		"src/.gitignore":            "generated.go\n",
		"src/generated.go":          "package src\n",
		"src/real.go":               "package src\n",
		"keep.txt":                  "visible\n",
	})
	w := New(root)

	for _, ignored := range []string{
		"secret.env",
		// No rule names this file; the directory above it is ignored, and git
		// prunes at the directory.
		"node_modules/pkg/index.js",
		// A nested .gitignore applies to its own subtree.
		"src/generated.go",
	} {
		if _, err := w.Read(ignored, 0, 0); err == nil ||
			!strings.Contains(err.Error(), "ignored by the project") {
			t.Errorf("Read(%q) err = %v, want it refused as ignored", ignored, err)
		}
	}
	for _, visible := range []string{"keep.txt", "src/real.go", ".gitignore"} {
		if _, err := w.Read(visible, 0, 0); err != nil {
			t.Errorf("Read(%q) = %v, want it to work", visible, err)
		}
	}

	// Pinning one is the user overriding their own project's rule, which /add
	// already accepts.
	w.Pinned = func(abs string) bool { return abs == filepath.Join(root, "secret.env") }
	if _, err := w.Read("secret.env", 0, 0); err != nil {
		t.Errorf("a pinned ignored file should be readable: %v", err)
	}
}

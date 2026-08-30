// Regression for the "path escapes the project root" bug: a file the user
// explicitly added to the chat must be editable even when it lives outside
// the project root — reached by a "../" relative path or through a symlinked
// directory — while a path the model invents on its own stays rejected.

package coder

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/workspace"
)

// TestAddFileThroughSymlinkedRootStaysRelative reproduces a symlinked checkout:
// the coder's Root is git's resolved path, but a file added by an absolute path
// in the symlink namespace (as the CLI's existingfile arg arrives) must still
// resolve to a clean repo-relative path — not "../link/..." — so the chat
// listing and git commits stay in-repo.
func TestAddFileThroughSymlinkedRootStaysRelative(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "internal", "foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := toolCoder(t, realDir)
	c.AddFile(filepath.Join(link, "internal", "foo.go"))

	if got, want := c.inchatRelativeFiles(), []string{"internal/foo.go"}; !slices.Equal(got, want) {
		t.Errorf("in-chat files = %v, want %v (clean repo-relative, no symlink escape)", got, want)
	}
}

func TestUnsafePathExemptsAddedFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	sibling := filepath.Join(base, "active", "dduckdns")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, root)
	const relUp = "../active/dduckdns/README.md"

	// Out of root and not added: rejected. A model-invented escape: rejected.
	if c.unsafePath(relUp) == "" {
		t.Error("out-of-root path not in chat should be rejected")
	}
	if c.unsafePath("../../etc/passwd") == "" {
		t.Error("model-invented out-of-root path should be rejected")
	}

	// The user adds it -> sanctioned target.
	c.AddFile(relUp)
	if reason := c.unsafePath(relUp); reason != "" {
		t.Errorf("added out-of-root file rejected: %s", reason)
	}
}

func TestUnsafePathExemptsAddedFileThroughSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	realDir := filepath.Join(base, "real", "dduckdns")
	for _, d := range []string{root, realDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(realDir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// active/ -> ../real, so active/dduckdns/README.md is textually in-root
	// but resolves outside it.
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(root, "active")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := toolCoder(t, root)
	const rel = "active/dduckdns/README.md"

	if c.unsafePath(rel) == "" {
		t.Error("symlink-escaping path not in chat should be rejected")
	}
	c.AddFile(rel)
	if reason := c.unsafePath(rel); reason != "" {
		t.Errorf("added symlink-escaping file rejected: %s", reason)
	}
}

// TestApplyEditsAddedOutOfRootFile is the end-to-end proof: an added
// out-of-root file actually gets written through the full apply pipeline.
func TestApplyEditsAddedOutOfRootFile(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	sibling := filepath.Join(base, "active", "dduckdns")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(sibling, "README.md")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, root)
	const rel = "../active/dduckdns/README.md"
	c.AddFile(rel)

	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		wholeFileWrite("call_1", rel, "new content\n"),
	}, map[string]string{}, &matchFailure)
	if matchFailure {
		t.Error("unexpected reflection editing a file the user deliberately added")
	}
	if len(edited) != 1 || edited[0] != rel {
		t.Errorf("edited = %v, want [%s]", edited, rel)
	}
	if got, _ := os.ReadFile(target); string(got) != "new content\n" {
		t.Errorf("out-of-root file not edited: %q", got)
	}
}

// TestPinnedAbsolutePathWritesToTheRealFile is the third instance of the drift
// contain() and unsafePath keep being warned about, and the first where the two
// agreed on the *decision* and disagreed on the *destination*.
//
// unsafePath exempts a pinned file before it tests for an absolute path, so an
// absolute path to a pinned file validates as safe. fullPath then joined it onto
// the root anyway, and filepath.Join(root, "/tmp/p/window.go") is
// "root/tmp/p/window.go" -- so the write landed in a shadow tree that mirrored
// the whole absolute path inside the project, the real file kept its old
// contents, and nothing reported an error. The model said it had fixed the bug;
// it had not.
//
// Found live: a model in the skills trial echoed back the absolute path
// Strument itself had just printed at it.
func TestPinnedAbsolutePathWritesToTheRealFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "window.go")
	if err := os.WriteFile(target, []byte("package w\n\nconst N = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, root)
	c.AddFile(target)

	abs, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	if reason := c.unsafePath(abs); reason != "" {
		t.Fatalf("a pinned file named absolutely was refused: %s", reason)
	}
	got := c.fullPath(abs)
	want := workspace.ResolveSymlinks(abs)
	if got != want {
		t.Errorf("fullPath(%q)\n =  %q\nwant %q", abs, got, want)
	}
	// The shape of the failure, stated separately: the resolved path must not
	// be the absolute path grafted onto the root.
	if strings.HasPrefix(got, workspace.ResolveSymlinks(root)+string(filepath.Separator)+"tmp") ||
		strings.Count(got, root) > 1 {
		t.Errorf("the path was joined onto the root, creating a shadow tree: %q", got)
	}
}

// An absolute spelling the model sends is resolved and checked, not refused:
// in-root absolute paths edit the file they name (the result names the
// root-relative form, so the model reads the same namespace every other tool
// result uses), out-of-root and .git ones are still refused. The write side is
// where the old flat refusal's neighbors lived — fullPath once joined an
// absolute path onto the root and wrote a shadow tree — so this goes through
// applyToolEdits rather than stopping at unsafePath.
func TestApplyEditsAcceptAbsolutePathInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "window.go"), []byte("package w\n\nconst N = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, root)
	abs := filepath.Join(root, "window.go")

	if reason := c.unsafePath(abs); reason != "" {
		t.Fatalf("in-root absolute path refused: %s", reason)
	}
	if c.unsafePath("/etc/passwd") == "" {
		t.Error("absolute path outside the root should be refused")
	}
	if c.unsafePath(filepath.Join(root, ".git", "config")) == "" {
		t.Error("absolute path into .git should be refused")
	}

	var matchFailure bool
	edited := c.applyToolEdits([]plannedEdit{
		{callID: "call_1", path: abs, search: "package w\n\nconst N = 1", replace: "package w\n\nconst N = 2"},
	}, map[string]string{}, &matchFailure)
	if matchFailure {
		t.Error("unexpected match failure editing by absolute path")
	}
	if len(edited) != 1 || edited[0] != "window.go" {
		t.Errorf("edited = %v, want [window.go] (the root-relative form)", edited)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package w\n\nconst N = 2\n" {
		t.Errorf("the real file was not written: %q", got)
	}
	// And no shadow tree: the only .go file under the root is the one that was
	// already there.
	var found []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") {
			rel, _ := filepath.Rel(root, p)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(found, []string{"window.go"}) {
		t.Errorf("files under the root = %v, want just [window.go]", found)
	}
}

// The relative case must keep working exactly as it did.
func TestRelativePathStillResolvesUnderTheRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, root)
	got := c.fullPath("internal/foo.go")
	want := workspace.ResolveSymlinks(filepath.Join(root, "internal", "foo.go"))
	if got != want {
		t.Errorf("fullPath(relative) = %q, want %q", got, want)
	}
}

// The same defect one layer up, through the code a write tool call actually
// runs. fullPath is where it lived, but a unit test on fullPath alone would go
// green for a fix that applyToolEdits then undoes on its own.
func TestWriteToAPinnedAbsolutePathHitsTheRealFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "window.go")
	const before = "package w\n\nconst N = 1\n"
	if err := os.WriteFile(target, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, root)
	c.AddFile(target)
	abs, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}

	results := map[string]string{}
	var matchFailure bool
	edited := c.applyToolEdits([]plannedEdit{{
		callID: "call_1", path: abs, create: true,
		replace: "package w\n\nconst N = 2\n",
	}}, results, &matchFailure)

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == before {
		t.Errorf("the real file was not written; result was %q, edited=%v",
			results["call_1"], edited)
	}
	// And no shadow tree: the only .go files under root are the one that was
	// already there.
	var found []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") {
			rel, _ := filepath.Rel(root, p)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(found, []string{"window.go"}) {
		t.Errorf("files under the root = %v, want just [window.go] "+
			"(a shadow tree means the absolute path was joined onto the root)", found)
	}
}

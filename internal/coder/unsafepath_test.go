// Regression for the "path escapes the project root" bug: a file the user
// explicitly added to the chat must be editable even when it lives outside
// the project root — reached by a "../" relative path or through a symlinked
// directory — while a path the model invents on its own stays rejected.

package coder

import (
	"os"
	"path/filepath"
	"testing"
)

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

	c := wholeModelCoder(t, root)
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

	c := wholeModelCoder(t, root)
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

	c := wholeModelCoder(t, root)
	const rel = "../active/dduckdns/README.md"
	c.AddFile(rel)

	edited, reflection := c.applyUpdates(rel + "\n```\nnew content\n```\n")
	if reflection != "" {
		t.Errorf("unexpected reflection: %q", reflection)
	}
	if len(edited) != 1 || edited[0] != rel {
		t.Errorf("edited = %v, want [%s]", edited, rel)
	}
	if got, _ := os.ReadFile(target); string(got) != "new content\n" {
		t.Errorf("out-of-root file not edited: %q", got)
	}
}

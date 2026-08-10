package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

func readOnlyCoder(t *testing.T) (*Coder, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ref.md")
	if err := os.WriteFile(p, []byte("reference\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(dir, &config.Model{Slug: "x"})
	c.Out = testOutput{t}
	return c, p
}

// /read-only asked the model not to edit and nothing enforced it: allowedToEdit
// consulted absFnames and gitignore, never absReadOnlyFnames, so a reference
// fell through to the final branch and was quietly promoted into the editable
// set. The command's one promise was a sentence in a prompt.
func TestReadOnlyFilesAreRefused(t *testing.T) {
	c, p := readOnlyCoder(t)
	c.AddReadOnlyFile(p)

	ok, why := c.allowedToEdit("ref.md", map[string]bool{})
	if ok {
		t.Fatal("a read-only file was accepted for editing")
	}
	if !strings.Contains(why, "read-only") {
		t.Errorf("the model should be told why: %q", why)
	}
	// The refusal must not leave the file editable for the next call either.
	if len(c.ChatFiles()) != 0 {
		t.Errorf("a refused file was promoted into the chat: %v", c.ChatFiles())
	}
}

// Read-only wins when a file is in both lists: the more restrictive answer is
// the safe one.
func TestReadOnlyWinsOverAdd(t *testing.T) {
	c, p := readOnlyCoder(t)
	c.AddFile(p)
	c.AddReadOnlyFile(p)

	if ok, _ := c.allowedToEdit("ref.md", map[string]bool{}); !ok {
		return
	}
	t.Error("an added-then-read-only file stayed editable")
}

// An unmarked file is unaffected: the refusal is scoped to what the user
// marked, not a new obstacle on the common path.
func TestOrdinaryFilesStillEditable(t *testing.T) {
	c, _ := readOnlyCoder(t)
	if ok, why := c.allowedToEdit("ref.md", map[string]bool{}); !ok {
		t.Errorf("an unmarked file was refused: %q", why)
	}
}

// A reference reached outside the project is what the feature exists for: the
// workspace tools are scoped to the root, so pinning is the only channel there
// is. Its contents must reach the prompt, and it must not become editable.
func TestOutsideReferenceIsPinnedButNotEditable(t *testing.T) {
	c, _ := readOnlyCoder(t)
	outside := filepath.Join(t.TempDir(), "api.md")
	if err := os.WriteFile(outside, []byte("GET /widgets returns 200.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddReadOnlyFile(outside)

	var sb strings.Builder
	for _, m := range c.formatChatChunks().readonlyFiles {
		sb.WriteString(m.Text())
	}
	if !strings.Contains(sb.String(), "GET /widgets returns 200.") {
		t.Errorf("an outside reference did not reach the prompt:\n%s", sb.String())
	}
	if ok, _ := c.allowedToEdit(outside, map[string]bool{}); ok {
		t.Error("an outside reference was editable")
	}
}

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

// The refusal has to give the real reason, and the tests above could not see
// whether it did: they called allowedToEdit directly, where the read-only check
// lives. On the path an actual edit takes, unsafePath runs first, and an
// out-of-tree reference failed containment before ever reaching that check —
// so the model was told "path escapes the project root", which is a fact about
// geography rather than about the pin. Live sessions show the difference:
// models read a containment error as an obstacle to route around (absolute
// path, then the shell, then hunting for a writable copy in the project), and
// a read-only error as an answer.
func TestOutsideReferenceIsRefusedForBeingReadOnly(t *testing.T) {
	c, _ := readOnlyCoder(t)
	outside := filepath.Join(t.TempDir(), "api.md")
	if err := os.WriteFile(outside, []byte("GET /widgets returns 200.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.AddReadOnlyFile(outside)

	rel := c.relFname(outside)
	if reason := c.unsafePath(rel); reason != "" {
		t.Fatalf("containment refused the pinned reference first, so the model never "+
			"learns it is read-only: %q", reason)
	}
	ok, why := c.allowedToEdit(rel, map[string]bool{})
	if ok {
		t.Fatal("an outside reference was editable")
	}
	if !strings.Contains(why, "read-only") {
		t.Errorf("the refusal should name the pin, not the path: %q", why)
	}
}

// TestReadOnlyNameIsUsable closes the loop the display change opens. Naming an
// out-of-tree pin absolutely is only an improvement if the name works: the
// prompt hands the model this string, so a tool call has to be able to act on
// it. That failed until contain was given the same pinned-first order
// unsafePath has always had.
//
// Both halves are asserted, because either alone is a trap. The prompt could
// name the file correctly and the read still refuse; the read could work and
// the prompt still name it some other way.
func TestReadOnlyNameIsUsable(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(root, "spec.md")
	if err := os.WriteFile(spec, []byte("NEEDLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &config.Model{Slug: "test"}
	m.SideModel = m
	c := New(proj, m)
	c.AddReadOnlyFile(spec)

	name := c.ReadOnlyFiles()[0]
	if !filepath.IsAbs(name) {
		t.Fatalf("out-of-tree pin named %q, want absolute", name)
	}

	// The prompt block names it the same way the UI does.
	block := c.readOnlyFilesContent()
	if !strings.Contains(block, name) {
		t.Errorf("read-only prompt block does not name the file %q:\n%s", name, block)
	}

	// And that name reads back through the tool layer.
	got, err := c.Files.Read(name, 0, 0)
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	if len(got.Lines) == 0 || got.Lines[0] != "NEEDLE" {
		t.Errorf("read %q returned %v", name, got.Lines)
	}

	// The exemption is exactly the pinned set, not "absolute paths now work".
	other := filepath.Join(root, "other.md")
	if err := os.WriteFile(other, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Files.Read(other, 0, 0); err == nil {
		t.Errorf("an unpinned absolute path was accepted: %s", other)
	}
}

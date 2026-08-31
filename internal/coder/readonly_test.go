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
	// An edit also must not add the file to the chat. Chat membership is what
	// the user pinned (/add): if the edit path could write to absFnames, the
	// system prompt would name the file as one "the user has pinned", resume
	// would restore it as a pin next session, and the model could widen what
	// isPinned exempts from containment one edit at a time.
	if got := c.ChatFiles(); len(got) != 0 {
		t.Errorf("an edit promoted its file into the chat: %v", got)
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

// TestReadOnlyNameIsUsable closes the loop the display rule opens. Whatever
// DisplayPath decides to call an out-of-tree pin, the prompt hands the model
// that string, so a tool call has to be able to act on it. The absolute form
// failed until contain was given the same pinned-first order unsafePath has
// always had.
//
// Both sides of the threshold are exercised, because they take different paths
// through contain: a sibling arrives as ../spec.md and hits the relative
// branch, a distant file arrives absolute and hits the branch this change
// added. And both halves are asserted for each, because either alone is a trap
// — the prompt could name the file correctly and the read still refuse, or the
// read could work and the prompt name it some other way.
func TestReadOnlyNameIsUsable(t *testing.T) {
	tests := []struct {
		name    string
		depth   []string // project root, under the temp dir
		wantAbs bool
	}{
		{name: "sibling stays relative", depth: []string{"proj"}},
		{name: "distant is absolute", depth: []string{"a", "b", "proj"}, wantAbs: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			proj := filepath.Join(append([]string{root}, tt.depth...)...)
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
			if got := filepath.IsAbs(name); got != tt.wantAbs {
				t.Fatalf("pin named %q, absolute = %v, want %v", name, got, tt.wantAbs)
			}

			// The prompt block names it the same way the UI does.
			if block := c.readOnlyFilesContent(); !strings.Contains(block, name) {
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

			// The exemption is exactly the pinned set, not "absolute paths now
			// work". An unpinned path outside the project and temp roots remains
			// refused.
			home, err := os.UserHomeDir()
			if err != nil {
				t.Skipf("cannot find a path outside the project and temp roots: %v", err)
			}
			file, err := os.CreateTemp(home, "strument-unpinned-absolute-path-")
			if err != nil {
				t.Skipf("cannot create a path outside the project and temp roots: %v", err)
			}
			other := file.Name()
			file.Close()
			t.Cleanup(func() { os.Remove(other) })
			if _, err := c.Files.Read(other, 0, 0); err == nil {
				t.Errorf("an unpinned absolute path was accepted: %s", other)
			}
		})
	}
}

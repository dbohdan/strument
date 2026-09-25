package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A search of one small directory finishes, however big the rest of the
// project is: the walk goes only where a match can be. Before, it walked
// everything and hit the entry limit first.
func TestScopedWalksIgnoreTheRestOfTheTree(t *testing.T) {
	root := t.TempDir()
	for i := range 50 {
		if err := os.MkdirAll(filepath.Join(root, "big"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "big", fmt.Sprintf("f%02d.txt", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "small", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "small", "sub", "a.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, Limits: Limits{MaxEntries: 20}}

	got, trunc, err := w.Glob("small/**/*.txt")
	if err != nil || trunc.Any() || len(got) != 1 || got[0] != "small/sub/a.txt" {
		t.Errorf("scoped glob = %v, %+v, %v; want [small/sub/a.txt] and no truncation", got, trunc, err)
	}
	if _, trunc, _ := w.Glob("**/a.txt"); !trunc.Entries {
		t.Error("an unscoped glob over more than the limit should still say it stopped")
	}
	res, err := w.Grep(GrepQuery{Pattern: "needle", Dir: "small", Mode: GrepFiles})
	if err != nil || res.Truncated.Any() || len(res.Files) != 1 {
		t.Errorf("scoped grep = %+v, %v; want one file and no truncation", res, err)
	}
	res, err = w.Grep(GrepQuery{Pattern: "needle", Glob: "small/**", Mode: GrepFiles})
	if err != nil || res.Truncated.Any() || len(res.Files) != 1 {
		t.Errorf("grep scoped by glob = %+v, %v; want one file and no truncation", res, err)
	}
}

func TestLiteralPrefixAndScope(t *testing.T) {
	for pattern, want := range map[string]string{
		"click/src/**/*.py": "click/src", "**/*.go": "", "*.md": "", "a/b.txt": "a/b.txt",
		"docs/[ab]*/x": "docs", "src/f?o/x": "src",
	} {
		if got := literalPrefix(pattern); got != want {
			t.Errorf("literalPrefix(%q) = %q, want %q", pattern, got, want)
		}
	}
	for _, tc := range []struct {
		rel, scope string
		dir, want  bool
	}{
		{"a", "a/b", true, true}, {"a/b", "a/b", true, true}, {"a/b/c.txt", "a/b", false, true},
		{"ab", "a/b", true, false}, {"a", "a/b", false, false}, {"x", "a", true, false},
	} {
		if got := inScope(tc.rel, tc.scope, tc.dir); got != tc.want {
			t.Errorf("inScope(%q, %q, dir=%v) = %v, want %v", tc.rel, tc.scope, tc.dir, got, tc.want)
		}
	}
}

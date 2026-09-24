package fixture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The handbook's sections are cited by label rather than by number, the way a
// LaTeX \ref cites a \label: the number is reading order and may be renumbered,
// the label is identity and may not. That only holds if something checks it.
// Numbers rotted silently — a renumbered section left every "§17" in the tree
// pointing at whatever moved into its place, and a Go comment saying so was as
// wrong as a prose one while looking just as authoritative.
//
// So this walks the repository, collects every citation of a handbook label,
// and fails if the label is not defined. It is the cheap half of the bargain
// labels make: readable references, provided the reference is checked.
var (
	anchorRE = regexp.MustCompile(`<a id="([a-z0-9-]+)"></a>`)
	citeRE   = regexp.MustCompile(`experimenting\.md#([a-z0-9-]+)`)
	// A bare "#label" in a Go comment, which is how a file that already named
	// the handbook on a previous line refers to a second section.
	bareRE = regexp.MustCompile(`\(#([a-z0-9-]+)\)`)
)

func TestHandbookLabelsResolve(t *testing.T) {
	root := repoRoot(t)
	handbook := filepath.Join(root, "doc", "experimenting.md")

	data, err := os.ReadFile(handbook)
	if err != nil {
		t.Fatalf("read handbook: %v", err)
	}
	defined := map[string]bool{}
	for _, m := range anchorRE.FindAllStringSubmatch(string(data), -1) {
		if defined[m[1]] {
			t.Errorf("label %q is defined twice; a citation cannot resolve it", m[1])
		}
		defined[m[1]] = true
	}
	if len(defined) < 20 {
		t.Fatalf("found only %d labels; the anchor format must have changed, "+
			"and this check is passing because it found nothing to check", len(defined))
	}

	cited := 0
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "reference", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".md" && ext != ".go" && ext != ".py" {
			return nil
		}
		// glm-review.md quotes a reviewer verbatim; its numbering is theirs.
		if strings.HasSuffix(path, "glm-review.md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		ms := citeRE.FindAllStringSubmatch(string(body), -1)
		if path == handbook {
			ms = append(ms, bareRE.FindAllStringSubmatch(string(body), -1)...)
		}
		for _, m := range ms {
			cited++
			if !defined[m[1]] {
				t.Errorf("%s cites handbook label %q, which is not defined in doc/experimenting.md", rel, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if cited < 40 {
		t.Fatalf("found only %d citations; the citation format must have changed, "+
			"and this check is passing because it found nothing to check", cited)
	}
	t.Logf("%d labels defined, %d citations checked", len(defined), cited)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Hand out a real directory, never a symlink. os.Getwd honors
			// an exported $PWD that names the same directory — fish
			// exports the logical path, and the test process inherits it
			// — so the working directory can arrive spelled through a
			// symlink (checkout -> actual), and this up-walk finds go.mod
			// on that spelling because os.Stat reads through symlinks.
			// filepath.Walk Lstats its root and descends only a real
			// directory: a symlinked root yields that one entry and
			// visits nothing, tripping the citation floor as "found only
			// 0 citations". The failure depends on the shell because not
			// every one exports PWD; resolving here makes the root
			// independent of both the shell and Getwd's answer.
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				dir = resolved
			}
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// The symlinked-path failure above is invisible in a shell that does not
// export PWD — bash didn't, fish does — so it is forced here rather than
// left to the environment: chdir through the symlink and export the
// logical PWD, which is the exact condition under which os.Getwd reports
// the symlinked spelling.
func TestRepoRootResolvesSymlinkedWorkingDirectory(t *testing.T) {
	realRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(realRoot, "go.mod"), []byte("module example.test/resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realRoot, "internal", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	logicalWd := filepath.Join(link, "internal", "fixture")

	t.Chdir(logicalWd)
	t.Setenv("PWD", logicalWd)

	// The precondition, so the test cannot pass vacuously if a future Go
	// stops honoring PWD: the whole point is the logical spelling.
	if d, err := os.Getwd(); err != nil || d != logicalWd {
		t.Fatalf("Getwd = %q, err = %v; want the logical %q to exercise the symlink path", d, err, logicalWd)
	}

	root := repoRoot(t)
	want, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Errorf("repoRoot = %q, want the resolved %q", root, want)
	}

	// The property the resolution exists for: the walk must descend. A
	// symlinked root visits exactly one entry — itself.
	visited := 0
	if err := filepath.Walk(root, func(string, os.FileInfo, error) error {
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited < 3 { // the root, go.mod, internal/, internal/fixture/
		t.Errorf("walk from repoRoot visited %d entries; a symlinked root visits exactly 1", visited)
	}
}

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
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

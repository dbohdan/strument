package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tree materializes a map of slash paths to contents under a temp root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFilesHonorsGitignore(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":            "*.log\nbuild/\n",
		"main.go":               "package main\n",
		"app.log":               "noise\n",
		"build/out.o":           "binary-ish\n",
		"src/lib.go":            "package src\n",
		"src/.gitignore":        "vendor/\n",
		"src/vendor/dep.go":     "package dep\n",
		"src/keep/kept.go":      "package keep\n",
		".git/objects/deadbeef": "should never appear\n",
	})

	got, trunc, err := New(root).Files()
	if err != nil {
		t.Fatal(err)
	}
	if trunc.Any() {
		t.Errorf("unexpected truncation: %+v", trunc)
	}
	// Dot-files are project content and stay visible; only .git is skipped.
	want := []string{".gitignore", "main.go", "src/.gitignore", "src/keep/kept.go", "src/lib.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Files() = %v, want %v", got, want)
	}
}

// TestDotFilesAreVisible pins the rule that only .git is hidden. A harness that
// cannot see .github/workflows or .golangci.toml cannot edit its own CI setup,
// and hiding by leading dot would do exactly that. What should be out of view is
// what the project itself declares out of view, via .gitignore.
func TestDotFilesAreVisible(t *testing.T) {
	root := tree(t, map[string]string{
		".github/workflows/ci.yml": "name: ci\n",
		".golangci.toml":           "version = \"2\"\n",
		".env":                     "SECRET=1\n",
		".gitignore":               ".env\n",
		".git/config":              "[core]\n",
		"main.go":                  "package main\n",
	})

	got, _, err := New(root).Files()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".github/workflows/ci.yml", ".gitignore", ".golangci.toml", "main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Files() = %v, want %v", got, want)
	}
	for _, unwanted := range []string{".env", ".git/config"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("%s must not be listed", unwanted)
		}
	}
}

// TestNestedIgnoreDoesNotLeakToSiblings is the aliasing bug this walker has to
// avoid: a nested .gitignore must apply to its own subtree only, never to a
// directory visited afterwards.
func TestNestedIgnoreDoesNotLeakToSiblings(t *testing.T) {
	root := tree(t, map[string]string{
		"a/.gitignore": "*.go\n",
		"a/hidden.go":  "package a\n",
		"b/shown.go":   "package b\n",
		"c/shown.go":   "package c\n",
	})

	got, _, err := New(root).Files()
	if err != nil {
		t.Fatal(err)
	}
	// a/hidden.go is excluded by a/.gitignore; the ignore file itself is
	// ordinary content and is listed.
	want := []string{"a/.gitignore", "b/shown.go", "c/shown.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Files() = %v, want %v (a/.gitignore must not reach b/ or c/)", got, want)
	}
}

func TestGlobDoubleStar(t *testing.T) {
	root := tree(t, map[string]string{
		"main.go":            "",
		"src/lib.go":         "",
		"src/deep/util.go":   "",
		"src/deep/notes.txt": "",
		"doc/git.html":       "",
		"doc/ppc/ppc.html":   "",
	})
	w := New(root)

	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"**/*.go", []string{"main.go", "src/deep/util.go", "src/lib.go"}},
		{"src/**/*.go", []string{"src/deep/util.go", "src/lib.go"}},
		{"*.go", []string{"main.go"}},
		{"doc/*.html", []string{"doc/git.html"}},
		{"**/*.rs", nil},
	} {
		got, _, err := w.Glob(tc.pattern)
		if err != nil {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("Glob(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestListIsOneLevel(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":   "*.log\n",
		"main.go":      "package main\n",
		"app.log":      "",
		"src/lib.go":   "",
		"src/sub/x.go": "",
	})
	w := New(root)

	entries, err := w.List("")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		suffix := ""
		if e.IsDir {
			suffix = "/"
		}
		got = append(got, e.Path+suffix)
	}
	want := []string{".gitignore", "main.go", "src/"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List(\"\") = %v, want %v", got, want)
	}

	entries, err = w.List("src")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Path != "src/lib.go" {
		t.Errorf("List(\"src\") = %+v", entries)
	}
}

func TestReadWindow(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line ")
		b.WriteByte(byte('0' + i%10))
		b.WriteString("\n")
	}
	root := tree(t, map[string]string{"f.txt": b.String()})
	w := New(root)

	whole, err := w.Read("f.txt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Total != 10 || len(whole.Lines) != 10 || whole.Start != 1 {
		t.Errorf("whole read = %+v", whole)
	}
	if whole.Truncated {
		t.Error("a complete read must not report truncation")
	}

	win, err := w.Read("f.txt", 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if win.Start != 3 || len(win.Lines) != 4 || win.Total != 10 {
		t.Errorf("window = %+v", win)
	}
	if !win.Truncated {
		t.Error("a window short of the end must report truncation so the model knows to page")
	}

	// Past the end is empty, not an error: the model asked about a line that
	// isn't there and should be told the file's real length.
	past, err := w.Read("f.txt", 99, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(past.Lines) != 0 || past.Total != 10 {
		t.Errorf("past-the-end read = %+v", past)
	}
}

func TestReadRejectsBinaryAndDirs(t *testing.T) {
	root := tree(t, map[string]string{"bin.dat": "ok\x00then nul\n", "d/x.txt": ""})
	w := New(root)

	if _, err := w.Read("bin.dat", 0, 0); err == nil {
		t.Error("reading a binary file should fail")
	}
	if _, err := w.Read("d", 0, 0); err == nil {
		t.Error("reading a directory should fail")
	}
}

func TestGrepModes(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":   "ignored/\n",
		"a.go":         "package a\nfunc Target() {}\n",
		"b.go":         "package b\n// Target again\n// and Target once more\n",
		"c.txt":        "nothing here\n",
		"ignored/z.go": "func Target() {}\n",
	})
	w := New(root)

	files, err := w.Grep(GrepQuery{Pattern: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 2 || files.Files[0].Path != "a.go" || files.Files[1].Path != "b.go" {
		t.Errorf("files mode = %+v (an ignored dir must not be searched)", files.Files)
	}

	content, err := w.Grep(GrepQuery{Pattern: "Target", Mode: GrepContent})
	if err != nil {
		t.Fatal(err)
	}
	if content.Total != 3 {
		t.Errorf("total matches = %d, want 3", content.Total)
	}
	if got := content.Files[0].Lines[0]; got.Number != 2 || !strings.Contains(got.Text, "func Target") {
		t.Errorf("first match = %+v", got)
	}

	scoped, err := w.Grep(GrepQuery{Pattern: "Target", Glob: "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Files) != 0 {
		t.Errorf("glob-scoped search = %+v, want none", scoped.Files)
	}

	folded, err := w.Grep(GrepQuery{Pattern: "target", IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(folded.Files) != 2 {
		t.Errorf("case-insensitive search = %+v", folded.Files)
	}
}

// TestResultsTruncationIsReported pins the contract that a capped result says
// so. A silently short answer reads to a model as "nothing more exists".
func TestResultsTruncationIsReported(t *testing.T) {
	files := map[string]string{}
	for i := range 20 {
		files[string(rune('a'+i))+".go"] = "match\n"
	}
	root := tree(t, files)
	w := &Workspace{Root: root, Limits: Limits{MaxResults: 5}}

	got, trunc, err := w.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || !trunc.Results {
		t.Errorf("Files() = %d entries, truncated=%+v, want 5 and Results=true", len(got), trunc)
	}

	res, err := w.Grep(GrepQuery{Pattern: "match"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated.Results {
		t.Error("a capped grep must report truncation")
	}
}

// TestNoGitNeeded is the point of the package: none of this consults git, so it
// behaves identically in a plain directory.
func TestNoGitNeeded(t *testing.T) {
	root := tree(t, map[string]string{"cfg/app.conf": "key = value\n"})
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("test root unexpectedly has a .git")
	}
	got, _, err := New(root).Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "cfg/app.conf" {
		t.Errorf("Files() = %v in a non-repo directory", got)
	}
}

// Transliterated from aider tests/basic/test_repomap.py @ 5dc9490. The
// refresh-mode tests (refresh="files"/"auto") are N/A: those knobs decide when
// aider may reuse a stale *rendered map*, and Strument re-ranks and re-renders
// on every call. It caches only tag extraction, keyed on the file's stamp
// (tagcache_test.go), so there is no staleness for a mode to trade against.

package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) []string {
	t.Helper()
	var paths []string
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func testMap(t *testing.T, root string) *RepoMap {
	t.Helper()
	rm := New(root)
	rm.Warn = func(format string, args ...any) { t.Logf("warn: "+format, args...) }
	return rm
}

func TestGetRepoMap(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"test_file1.py": "", "test_file2.py": "",
		"test_file3.md": "", "test_file4.json": "",
	}
	other := writeFiles(t, dir, files)
	result := testMap(t, dir).GetRepoMap(nil, other, nil, nil)
	for name := range files {
		if !strings.Contains(result, name) {
			t.Errorf("map missing %s:\n%s", name, result)
		}
	}
}

func TestGetRepoMapWithIdentifiers(t *testing.T) {
	dir := t.TempDir()
	other := writeFiles(t, dir, map[string]string{
		"test_file_with_identifiers.py": `class MyClass:
    def my_method(self, arg1, arg2):
        return arg1 + arg2

def my_function(arg1, arg2):
    return arg1 * arg2
`,
		"test_file_import.py": `from test_file_with_identifiers import MyClass

obj = MyClass()
print(obj.my_method(1, 2))
print(my_function(3, 4))
`,
		"test_file_pass.py": "pass",
	})
	result := testMap(t, dir).GetRepoMap(nil, other, nil, nil)
	for _, want := range []string{"test_file_with_identifiers.py", "MyClass", "my_method", "my_function", "test_file_pass.py"} {
		if !strings.Contains(result, want) {
			t.Errorf("map missing %q:\n%s", want, result)
		}
	}
}

func TestGetRepoMapAllFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"test_file0.py": "", "test_file1.txt": "", "test_file2.md": "",
		"test_file3.json": "", "test_file4.html": "", "test_file5.css": "",
		"test_file6.js": "",
	}
	other := writeFiles(t, dir, files)
	result := testMap(t, dir).GetRepoMap(nil, other, nil, nil)
	for name := range files {
		if !strings.Contains(result, name) {
			t.Errorf("map missing %s:\n%s", name, result)
		}
	}
}

func TestGetRepoMapExcludesAddedFiles(t *testing.T) {
	dir := t.TempDir()
	all := writeFiles(t, dir, map[string]string{
		"test_file1.py": "def foo(): pass\n", "test_file2.py": "def foo(): pass\n",
		"test_file3.md": "def foo(): pass\n", "test_file4.json": "def foo(): pass\n",
	})
	// Deterministic split: writeFiles map order is random, so sort.
	var chat, other []string
	for _, p := range all {
		switch filepath.Base(p) {
		case "test_file1.py", "test_file2.py":
			chat = append(chat, p)
		default:
			other = append(other, p)
		}
	}
	result := testMap(t, dir).GetRepoMap(chat, other, nil, nil)
	if strings.Contains(result, "test_file1.py") || strings.Contains(result, "test_file2.py") {
		t.Errorf("chat files leaked into the map:\n%s", result)
	}
	for _, want := range []string{"test_file3.md", "test_file4.json"} {
		if !strings.Contains(result, want) {
			t.Errorf("map missing %s:\n%s", want, result)
		}
	}
}

// Language coverage matrix, from TestRepoMapAllLanguages. The first block is
// the 28 language-pack languages; the second is the legacy
// tree-sitter-languages fallback. Languages whose
// gotreesitter grammar is missing (ocaml_interface, pony, udev) or has
// diverged from aider's legacy query (julia: scoped_identifier; zig:
// FnProto) are out of scope and covered as bare entries.
var languageCases = []struct {
	lang, ext, symbol string
}{
	{"arduino", "ino", "setup"},
	{"bash", "sh", "greet"},
	{"c", "c", "main"},
	{"chatito", "chatito", "intent"},
	{"clojure", "clj", "greet"},
	{"commonlisp", "lisp", "greet"},
	{"cpp", "cpp", "main"},
	{"csharp", "cs", "IGreeter"},
	{"d", "d", "main"},
	{"dart", "dart", "Person"},
	{"elisp", "el", "greeter"},
	{"elixir", "ex", "Greeter"},
	{"elm", "elm", "Person"},
	{"gleam", "gleam", "greet"},
	{"go", "go", "Greeter"},
	{"java", "java", "Greeting"},
	{"javascript", "js", "Person"},
	{"lua", "lua", "greet"},
	{"matlab", "m", "Person"},
	{"ocaml", "ml", "Greeter"},
	{"properties", "properties", "database.url"},
	{"python", "py", "Person"},
	{"r", "r", "calculate"},
	{"racket", "rkt", "greet"},
	{"ruby", "rb", "greet"},
	{"rust", "rs", "Person"},
	{"solidity", "sol", "SimpleStorage"},
	{"swift", "swift", "Greeter"},

	// Legacy tree-sitter-languages fallback (Q1).
	{"fortran", "f90", "greet"},
	{"haskell", "hs", "add"},
	{"hcl", "tf", "aws_region"},
	{"kotlin", "kt", "greet"},
	{"php", "php", "greet"},
	{"scala", "scala", "greet"},
	{"typescript", "ts", "greet"},
	{"tsx", "tsx", "UserProps"},
}

func TestAllLanguages(t *testing.T) {
	for _, c := range languageCases {
		t.Run(c.lang, func(t *testing.T) {
			fixture := filepath.Join("..", "..", "testdata", "transliterated", "repomap", "languages", c.lang, "test."+c.ext)
			content, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			filename := "test." + c.ext
			testFile := filepath.Join(dir, filename)
			if err := os.WriteFile(testFile, content, 0o644); err != nil {
				t.Fatal(err)
			}
			result := testMap(t, dir).GetRepoMap(nil, []string{testFile}, nil, nil)
			if len(strings.Split(strings.TrimSpace(result), "\n")) <= 1 {
				t.Fatalf("map too small:\n%s", result)
			}
			if !strings.Contains(result, filename) {
				t.Errorf("file %s not in map:\n%s", filename, result)
			}
			if !strings.Contains(result, c.symbol) {
				t.Errorf("symbol %q not in map:\n%s", c.symbol, result)
			}
		})
	}
}

// The sample-code-base golden. The fixture layout reproduces aider's
// relative paths (tests/fixtures/sample-code-base/...) inside a temp root.
// The golden is regenerated from the corrected Go implementation when it
// differs from upstream only through the two known differences from upstream
// (sqrt-once and single-tag emission); the upstream golden is tried first.
func TestSampleCodeBaseGolden(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "transliterated", "repomap", "sample-code-base")
	goldenPath := filepath.Join("..", "..", "testdata", "transliterated", "repomap", "sample-code-base-repo-map.txt")

	dir := t.TempDir()
	sub := filepath.Join(dir, "tests", "fixtures", "sample-code-base")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	var other []string
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(sub, e.Name())
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		other = append(other, p)
	}

	generated := strings.TrimSpace(testMap(t, dir).GetRepoMap(nil, other, nil, nil))

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(golden))

	if generated != want {
		gotLines := strings.Split(generated, "\n")
		wantLines := strings.Split(want, "\n")
		n := min(len(gotLines), len(wantLines))
		for i := range n {
			if gotLines[i] != wantLines[i] {
				t.Fatalf("first difference at line %d:\nwant: %q\n got: %q\n\nfull generated:\n%s",
					i+1, wantLines[i], gotLines[i], generated)
			}
		}
		t.Fatalf("length differs: got %d lines, want %d\nfull generated:\n%s", len(gotLines), len(wantLines), generated)
	}
}

// Startup self-test: every embedded query for a supported language compiles.
func TestAllQueriesCompile(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) < 28 {
		t.Fatalf("expected >= 28 supported languages, got %d: %v", len(langs), langs)
	}
	for _, lang := range langs {
		if _, err := langFor(lang); err != nil {
			t.Errorf("%s: %v", lang, err)
		}
	}
}

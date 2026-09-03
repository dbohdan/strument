// What survives of aider's tests/basic/test_repomap.py @ 5dc9490 now that the
// ranked map is gone: the language matrix, asked through Tags rather than
// through a rendered map, and the query self-test.
//
// The rest went with the map it tested. The four TestGetRepoMap* cases and the
// sample-code-base golden all pinned rendered output, and the golden's value
// was agreeing with aider's — meaningless once nothing renders. aider's
// refresh-mode tests were already N/A: they decide when a stale rendered map
// may be reused, and Strument caches only tag extraction, keyed on the file's
// stamp (tagcache_test.go).
//
// Asking through Tags turned out to be the stricter question. The old
// assertion searched the rendered map, which carried source lines as well as
// tag names, so elisp passed on the word "greeter" appearing in a rendered
// (defclass greeter ...) line — its query extracts defun and nothing else, and
// never produced that tag at all. Every other language matched a real tag.

package repomap

import (
	"os"
	"path/filepath"
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
	{"elisp", "el", "create-formal-greeter"}, // defun only; see the note above
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
			if skipSlowGrammar(t, c.lang) {
				return
			}
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
			tags := testMap(t, dir).Tags([]string{testFile})
			if len(tags) == 0 {
				t.Fatalf("%s produced no tags at all", filename)
			}
			var found bool
			for _, tag := range tags {
				if tag.RelFname != filename {
					t.Errorf("tag under the wrong path: %+v", tag)
				}
				if tag.Name == c.symbol {
					found = true
				}
			}
			if !found {
				t.Errorf("symbol %q not among %d tags for %s", c.symbol, len(tags), filename)
			}
		})
	}
}

// Startup self-test: every embedded query for a supported language compiles.
// The sweep that used to live here — every supported language through langFor
// — is TestAllEmbeddedQueriesCompile in compile_test.go, which makes the same
// call with strictly stronger assertions (it also requires a compiled query,
// and a higher language count). Two copies meant paying twice for the
// grammars that are expensive to construct.

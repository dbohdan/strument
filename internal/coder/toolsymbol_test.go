package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
)

// symbolEnv builds a coder over a small Go project with a repo map wired up,
// which is what gates the symbol tool.
func symbolEnv(t *testing.T, files map[string]string) (*Coder, *captureOut) {
	t.Helper()
	c, out := observeEnv(t, files)
	c.RepoMap = repomap.New(c.Root)
	return c, out
}

const symbolLib = `package lib

// Greet says hello. Greet is named twice in this comment: Greet.
func Greet(name string) string {
	return "hello " + name
}

func caller() string {
	return Greet("world")
}
`

// TestSymbolFindsDefinitionsNotText is the whole difference from grep. The
// comment above Greet names it three times and the tool reports none of them —
// a text search cannot make that distinction, which is why the two tools do not
// overlap and why their descriptions say so.
func TestSymbolFindsDefinitionsNotText(t *testing.T) {
	c, out := symbolEnv(t, map[string]string{"lib.go": symbolLib})

	got := c.runSymbol(call("symbol", `{"name":"Greet"}`))
	if !strings.Contains(got, "lib.go:4") {
		t.Errorf("the definition was not found:\n%s", got)
	}
	if strings.Contains(got, "lib.go:3") {
		t.Errorf("a mention in a comment was reported as a definition:\n%s", got)
	}
	if n := strings.Count(got, "lib.go:"); n != 1 {
		t.Errorf("reported %d sites, want exactly 1:\n%s", n, got)
	}
	if joined := strings.Join(out.lines, "\n"); !strings.Contains(joined, "Looked up Greet") {
		t.Errorf("the lookup was not announced:\n%s", joined)
	}
}

func TestSymbolFindsReferences(t *testing.T) {
	c, _ := symbolEnv(t, map[string]string{"lib.go": symbolLib})

	got := c.runSymbol(call("symbol", `{"name":"Greet","kind":"reference"}`))
	if !strings.Contains(got, "lib.go:9") {
		t.Errorf("the call site was not found:\n%s", got)
	}
}

const symbolCallers = `package lib

import "fmt"

type Store struct{ n int }

func Target() int { return 1 }

func fromFunc() int {
	return Target()
}

func (s *Store) fromMethod() int {
	return Target()
}

var atFileScope = Target()

func printer() {
	fmt.Println(Target())
}
`

// TestSymbolNamesTheEnclosingFunction: a list of coordinates says where to
// look, a function name says what is doing the looking. The method carries its
// receiver, because "fromMethod" alone does not locate anything in a project
// with several of them.
func TestSymbolNamesTheEnclosingFunction(t *testing.T) {
	c, _ := symbolEnv(t, map[string]string{"lib.go": symbolCallers})

	got := c.runSymbol(call("symbol", `{"name":"Target","kind":"reference"}`))
	for _, want := range []string{
		"in fromFunc",
		"in Store.fromMethod",
		"in printer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// TestSymbolSaysNothingAboutAReferenceOutsideAFunction pins the half that
// matters more. A reference at file scope has no enclosing function, and the
// answer must leave it unannotated rather than reach for the nearest name
// above it — a wrong function name sends a reader somewhere real and wrong.
func TestSymbolSaysNothingAboutAReferenceOutsideAFunction(t *testing.T) {
	c, _ := symbolEnv(t, map[string]string{"lib.go": symbolCallers})

	got := c.runSymbol(call("symbol", `{"name":"Target","kind":"reference"}`))
	for line := range strings.SplitSeq(got, "\n") {
		if !strings.HasPrefix(line, "lib.go:17") {
			continue
		}
		if strings.Contains(line, " in ") {
			t.Errorf("a file-scope reference was given an enclosing function: %q", line)
		}
		return
	}
	t.Errorf("the file-scope reference on line 17 was not reported at all:\n%s", got)
}

// A name can be extracted twice by overlapping query patterns, so the dedup has
// to key on the annotation as well as the site — otherwise one reference shows
// up once with a function name and once without.
func TestSymbolReportsEachSiteOnce(t *testing.T) {
	c, _ := symbolEnv(t, map[string]string{"lib.go": symbolCallers})

	got := c.runSymbol(call("symbol", `{"name":"Target","kind":"reference"}`))
	seen := map[string]int{}
	for line := range strings.SplitSeq(got, "\n") {
		if site, _, ok := strings.Cut(strings.TrimSpace(line), " "); ok && strings.HasPrefix(site, "lib.go:") {
			seen[site]++
		} else if strings.HasPrefix(line, "lib.go:") {
			seen[strings.TrimSpace(line)]++
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no sites were reported:\n%s", got)
	}
	for site, n := range seen {
		if n != 1 {
			t.Errorf("%s reported %d times:\n%s", site, n, got)
		}
	}
}

// TestSymbolSaysWhenItFindsNothing: silence would read as "this name does not
// exist", when the truth may be that no grammar covers the file it lives in.
// The answer points at grep rather than leaving the model stuck.
func TestSymbolSaysWhenItFindsNothing(t *testing.T) {
	c, _ := symbolEnv(t, map[string]string{
		"lib.go":    symbolLib,
		"notes.txt": "func Missing() {}\n",
	})

	got := c.runSymbol(call("symbol", `{"name":"Missing"}`))
	if !strings.Contains(got, "No place") || !strings.Contains(got, "grep") {
		t.Errorf("result = %q", got)
	}
}

func TestSymbolRejectsBadArguments(t *testing.T) {
	c, _ := symbolEnv(t, map[string]string{"lib.go": symbolLib})

	if got := c.runSymbol(call("symbol", `{}`)); !strings.Contains(got, "name") {
		t.Errorf("a missing name must say so: %q", got)
	}
	if got := c.runSymbol(call("symbol", `{"name":"Greet","kind":"sideways"}`)); !strings.Contains(got, "Unknown kind") {
		t.Errorf("an unknown kind must say so: %q", got)
	}
}

// TestSymbolOfferedOnlyWithGrammars: the tool reads the same tree-sitter layer
// the repo map is built from, so without that layer it must not be advertised.
func TestSymbolOfferedOnlyWithGrammars(t *testing.T) {
	c, _ := symbolEnv(t, nil)
	c.editFormat = "tool"

	if !hasTool(c.toolDefs(), toolSymbol) {
		t.Error("symbol not offered with a repo map wired up")
	}
	c.RepoMap = nil
	if hasTool(c.toolDefs(), toolSymbol) {
		t.Error("symbol offered without the parse layer behind it")
	}
}

func hasTool(defs []llm.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

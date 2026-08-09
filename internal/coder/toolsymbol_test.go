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

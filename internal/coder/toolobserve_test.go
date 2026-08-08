package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/workspace"
)

// observeEnv builds a coder over a temp tree with just enough wired up to run
// the observation tools, which need no client, repo, or confirmer.
func observeEnv(t *testing.T, files map[string]string) (*Coder, *captureOut) {
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
	out := &captureOut{}
	return &Coder{Root: root, Out: out, Files: workspace.New(root)}, out
}

func call(name, args string) llm.ToolCall {
	return llm.ToolCall{ID: "call_1", Name: name, Arguments: args}
}

// TestReadNumbersLinesAndPages covers the read result's two jobs: give the
// model stable line referents, and — when the window stops short — say so with
// the offset that continues it.
func TestReadNumbersLinesAndPages(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("body\n")
	}
	c, _ := observeEnv(t, map[string]string{"f.txt": b.String()})

	whole := c.runRead(call("read", `{"path":"f.txt"}`))
	if !strings.Contains(whole, " 1\tbody") || !strings.Contains(whole, "10\tbody") {
		t.Errorf("read result is not line-numbered:\n%s", whole)
	}
	if strings.Contains(whole, "for more") {
		t.Errorf("a complete read must not offer paging:\n%s", whole)
	}

	win := c.runRead(call("read", `{"path":"f.txt","offset":3,"limit":4}`))
	if !strings.Contains(win, "Read from offset 7 for more") {
		t.Errorf("a short window must name the next offset:\n%s", win)
	}
	if !strings.Contains(win, "(10 lines)") {
		t.Errorf("the result must carry the file's real length:\n%s", win)
	}
}

func TestReadReportsMissingFileToTheModel(t *testing.T) {
	c, _ := observeEnv(t, nil)
	got := c.runRead(call("read", `{"path":"nope.txt"}`))
	if !strings.Contains(got, "Could not read nope.txt") {
		t.Errorf("result = %q, want a model-facing failure", got)
	}
}

func TestReadRequiresPath(t *testing.T) {
	c, _ := observeEnv(t, nil)
	if got := c.runRead(call("read", `{}`)); !strings.Contains(got, "path") {
		t.Errorf("result = %q, want it to name the missing argument", got)
	}
}

func TestGrepModesAndScoping(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go":  "package a\nfunc Target() {}\n",
		"b.go":  "package b\n// Target\n",
		"c.txt": "Target here too\n",
	})

	files := c.runGrep(call("grep", `{"pattern":"Target"}`))
	if !strings.Contains(files, "a.go") || !strings.Contains(files, "3 matches in 3 files") {
		t.Errorf("files mode:\n%s", files)
	}

	content := c.runGrep(call("grep", `{"pattern":"Target","mode":"content"}`))
	if !strings.Contains(content, "a.go:2: func Target() {}") {
		t.Errorf("content mode must carry path:line: text:\n%s", content)
	}

	scoped := c.runGrep(call("grep", `{"pattern":"Target","glob":"*.go"}`))
	if strings.Contains(scoped, "c.txt") {
		t.Errorf("glob scoping leaked a non-matching file:\n%s", scoped)
	}

	none := c.runGrep(call("grep", `{"pattern":"zzz"}`))
	if !strings.Contains(none, "No matches") {
		t.Errorf("empty result = %q", none)
	}

	bad := c.runGrep(call("grep", `{"pattern":"("}`))
	if !strings.Contains(bad, "not valid") {
		t.Errorf("a bad regexp must come back as a fixable message, got %q", bad)
	}
}

func TestGlobAndLS(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"main.go":       "",
		"src/lib.go":    "",
		"src/deep/x.go": "",
		"src/notes.txt": "",
		"vendor/dep.go": "",
		".gitignore":    "vendor/\n",
	})

	glob := c.runGlob(call("glob", `{"pattern":"src/**/*.go"}`))
	if !strings.Contains(glob, "src/lib.go") || !strings.Contains(glob, "src/deep/x.go") {
		t.Errorf("glob:\n%s", glob)
	}
	if strings.Contains(glob, "notes.txt") {
		t.Errorf("glob matched the wrong extension:\n%s", glob)
	}

	ls := c.runLS(call("ls", `{"path":"src"}`))
	if !strings.Contains(ls, "src/deep/") || !strings.Contains(ls, "src/lib.go") {
		t.Errorf("ls must mark directories with a trailing slash:\n%s", ls)
	}

	root := c.runLS(call("ls", `{}`))
	if !strings.Contains(root, "the project root") {
		t.Errorf("ls with no path should describe the root:\n%s", root)
	}
	if strings.Contains(root, "vendor") {
		t.Errorf("ls listed an ignored directory:\n%s", root)
	}
}

// TestVerifyRunsNamedCheck is the security-relevant one: the tool takes a name,
// looks the argv up in the config, and runs that — nothing the model sends can
// change what executes.
func TestVerifyRunsNamedCheck(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{
		{Name: "ok", Argv: []string{"true"}},
		{Name: "echo", Argv: []string{"echo", "hello from verify"}},
	}

	got := c.runVerify(t.Context(), call("verify", `{"name":"echo"}`))
	if !strings.Contains(got, "hello from verify") || !strings.Contains(got, "Exit status: 0") {
		t.Errorf("verify result:\n%s", got)
	}

	unknown := c.runVerify(t.Context(), call("verify", `{"name":"nope"}`))
	if !strings.Contains(unknown, "no check named") || !strings.Contains(unknown, "ok, echo") {
		t.Errorf("an unknown name must list the real ones, got:\n%s", unknown)
	}
}

// TestVerifyStopsAtFirstFailure pins the ordering contract: checks run in the
// order the user declared, and a failure ends the run so the user's fast checks
// can shield the slow ones.
func TestVerifyStopsAtFirstFailure(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{
		{Name: "fails", Argv: []string{"false"}},
		{Name: "never", Argv: []string{"echo", "should not run"}},
	}

	got := c.runVerify(t.Context(), call("verify", `{}`))
	if strings.Contains(got, "should not run") {
		t.Errorf("a later check ran after a failure:\n%s", got)
	}
	if !strings.Contains(got, "Stopped here") {
		t.Errorf("the model must be told the run stopped early:\n%s", got)
	}
}

func TestVerifyWithoutConfigSaysSo(t *testing.T) {
	c, _ := observeEnv(t, nil)
	if got := c.runVerify(t.Context(), call("verify", `{}`)); !strings.Contains(got, "No checks are configured") {
		t.Errorf("result = %q", got)
	}
}

// TestObservationToolsAnnounceThemselves confirms each read-only call prints a
// one-line outcome, so the user watching the scroll sees what the model looked
// at rather than a silent gap between diffs.
func TestObservationToolsAnnounceThemselves(t *testing.T) {
	c, out := observeEnv(t, map[string]string{"a.go": "package a\n"})

	c.runRead(call("read", `{"path":"a.go"}`))
	c.runGrep(call("grep", `{"pattern":"package"}`))
	c.runGlob(call("glob", `{"pattern":"*.go"}`))
	c.runLS(call("ls", `{}`))

	joined := strings.Join(out.lines, "\n")
	for _, want := range []string{"Read a.go", "Searched for package", "Matched 1 file", "Listed the project root"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

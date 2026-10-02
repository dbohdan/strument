package coder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// answerConfirmer answers each prompt from a script and records it.
type answerConfirmer struct {
	answers []ConfirmResult
	got     []ConfirmRequest
}

func (a *answerConfirmer) Confirm(req ConfirmRequest) ConfirmResult {
	a.got = append(a.got, req)
	if len(a.answers) == 0 {
		return ConfirmResult{}
	}
	res := a.answers[0]
	a.answers = a.answers[1:]
	return res
}

func pathCall(t *testing.T, tool, path string) llm.ToolCall {
	t.Helper()
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return llm.ToolCall{ID: "c", Name: tool, Arguments: string(args)}
}

func mkfile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A read outside the project asks, and the answer decides: "y" covers that
// file for the run, "a" everything under its directory, "n" nothing. A symlink
// under a granted directory that leads out of it is asked about again, a
// secret-shaped file is refused without a question, and the home directory is
// never offered as an "a".
func TestReadsOutsideTheProjectAsk(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	proj := filepath.Join(base, "proj")
	cfgDir := filepath.Join(home, ".config", "tool")
	mkfile(t, filepath.Join(proj, "main.go"), "package main\n")
	mkfile(t, filepath.Join(cfgDir, "config.star"), "x = 1\n")
	mkfile(t, filepath.Join(cfgDir, "other.star"), "y = 2\n")
	mkfile(t, filepath.Join(cfgDir, "sub", "deep.star"), "z = 3\n")
	mkfile(t, filepath.Join(home, ".netrc"), "machine x password hunter2\n")
	mkfile(t, filepath.Join(home, "notes.txt"), "notes\n")
	mkfile(t, filepath.Join(base, "elsewhere", "far.txt"), "far\n")
	if err := os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(cfgDir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := toolCoder(t, proj)
	conf := &answerConfirmer{}
	c.Confirm = conf
	// Spelled relative to the project, because t.TempDir is under the
	// temporary directory, whose grant covers absolute spellings only. A
	// "../" path out of the project reaches the same gate an absolute one
	// elsewhere does.
	rel := func(p string) string {
		r, err := filepath.Rel(proj, p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	read := func(p string) string {
		out, _ := c.runRead(pathCall(t, toolRead, rel(p)))
		return out
	}

	conf.answers = []ConfirmResult{{Yes: true}}
	if out := read(filepath.Join(cfgDir, "config.star")); !strings.Contains(out, "x = 1") {
		t.Fatalf("approved read: %q", out)
	}
	if len(conf.got) != 1 || conf.got[0].Grant != GrantReadOutside || conf.got[0].Scope != cfgDir {
		t.Fatalf("prompt = %+v", conf.got)
	}
	if read(filepath.Join(cfgDir, "config.star")); len(conf.got) != 1 {
		t.Error("a second read of an approved file asked again")
	}

	conf.answers = []ConfirmResult{{Always: true}}
	read(filepath.Join(cfgDir, "other.star"))
	if out := read(filepath.Join(cfgDir, "sub", "deep.star")); !strings.Contains(out, "z = 3") || len(conf.got) != 2 {
		t.Errorf("a file under an \"a\" directory: asked %d times, read %q", len(conf.got), out)
	}

	conf.answers = []ConfirmResult{{}}
	out := read(filepath.Join(cfgDir, "link", "far.txt"))
	if len(conf.got) != 3 || !strings.Contains(conf.got[2].Path, "→") {
		t.Errorf("a symlink out of the granted directory was not asked about with its target: %+v", conf.got)
	}
	if !strings.Contains(out, "chose not to read") {
		t.Errorf("declined read: %q", out)
	}

	if out := read(filepath.Join(home, ".netrc")); !strings.Contains(out, "secret-file pattern") || len(conf.got) != 3 {
		t.Errorf("secret file: asked %d times, answer %q", len(conf.got), out)
	}

	conf.answers = []ConfirmResult{{Yes: true}}
	read(filepath.Join(home, "notes.txt"))
	if last := conf.got[len(conf.got)-1]; last.Group != "" || last.Scope != "" {
		t.Errorf("a file in the home directory offered \"a\" over all of it: %+v", last)
	}

	conf.answers = []ConfirmResult{{Yes: true}}
	if out := c.runLS(pathCall(t, toolLS, rel(filepath.Join(base, "elsewhere")))); !strings.Contains(out, "far.txt") {
		t.Errorf("approved ls: %q", out)
	}

	// strument tool asks no one: the refusal it always gave.
	insp := &Inspector{Root: proj, Files: c.Files, Out: DiscardReporter{}}
	c.outside = outsideGrants{}
	if out, _ := insp.runRead(pathCall(t, toolRead, rel(filepath.Join(base, "elsewhere", "far.txt")))); !strings.Contains(out, "outside the project root") {
		t.Errorf("Inspector without AskOutside: %q", out)
	}
}

// Scratch-repo tests for /diff and /undo (phase 8 oracle).

package repl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/llm"
)

func initScratchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.name", "Scratch User"},
		{"git", "config", "user.email", "scratch@example.com"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// Two commits so HEAD has a parent: the pre-turn /undo must hit the
	// session gate, not the first-commit gate.
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "README"},
		{"git", "commit", "-q", "-m", "initial commit"},
		{"git", "add", "main.txt"},
		{"git", "commit", "-q", "-m", "base commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return root
}

// editCommitStub replays an edit tool call against main.txt, the model's
// commit of it, and the closing answer that ends the turn — each a send of its
// own, since a reply ending in a tool call is mid-sentence and the harness
// re-sends on the result.
func editCommitStub() *fixture.StreamStub {
	args := `{"path":"main.txt","old_string":"hello world\n","new_string":"hello strument\n"}`
	return &fixture.StreamStub{Turns: []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: "ToolCall", ToolIndex: 0, ToolID: "call_1", ToolName: "edit", ToolArgs: args},
			{Kind: "Finish", FinishReason: "tool_calls"},
		}},
		{Events: []fixture.Event{
			{Kind: "ToolCall", ToolIndex: 0, ToolID: "call_2", ToolName: "commit", ToolArgs: `{"subject":"feat: greet strument"}`},
			{Kind: "Finish", FinishReason: "tool_calls"},
		}},
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "Changed the greeting."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
	}}
}

// editTurn is one turn of the stub: an edit call, then the answer that ends it.
func editTurn(callID, path, from, to string) []fixture.Turn {
	args := `{"path":"` + path + `","old_string":"` + from + `","new_string":"` + to + `"}`
	return []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: "ToolCall", ToolIndex: 0, ToolID: callID, ToolName: "edit", ToolArgs: args},
			{Kind: "Finish", FinishReason: "tool_calls"},
		}},
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "Done."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
	}
}

// TestUndoWithoutGitSession is the no-repository half of /undo. There is no
// commit to move away from, so the turn's snapshot is the whole record — and
// this is the case that makes Strument usable on a live configuration directory
// or under another SCM.
func TestUndoWithoutGitSession(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = &fixture.StreamStub{Turns: editTurn("call_1", "main.txt", `hello world\n`, `hello strument\n`)}

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		Git:        nil, // --no-git, or simply not a repository
		ModelAlias: "test",
		Stdin:      strings.NewReader("change the greeting\n/undo\n/exit\n"),
		Stdout:     out,
		Stderr:     out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cdr.Confirm = coder.AutoConfirmer{Granted: coder.StaticGrants(map[string]bool{coder.GrantBash: true, coder.GrantSteps: true, coder.GrantContext: true}), Fallback: r.Confirmer()}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "main.txt")); string(data) != "hello world\n" {
		t.Errorf("main.txt after undo = %q, want the pre-turn contents", data)
	}
	got := out.String()
	if !strings.Contains(got, "Undid the last turn's edits:") || !strings.Contains(got, "main.txt") {
		t.Errorf("output does not report the undo:\n%s", got)
	}

	// A second /undo has nothing left, and must say so rather than pretend.
	if _, err := cdr.UndoLastTurn(); err == nil {
		t.Error("want an error undoing twice")
	}
}

func TestModelSwitchUpdatesTrailer(t *testing.T) {
	root := initScratchRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	small := testModel()
	small.Slug = "vendor/small:nitro" // readable name drops prefix + :nitro
	big := testModel()
	big.Slug = "vendor/big-model"
	big.DisplayName = "Big Model"

	g.CommitTrailer = gitrepo.Trailer(small.ReadableName())
	client := &fixture.StreamStub{}

	cdr := coder.New(root, small)
	cdr.Client = client
	cdr.Repo = g

	cfg := testConfig(small)
	cfg.Models["big"] = big

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     cfg,
		Git:        g,
		ModelAlias: "test",
		MakeClient: func(*config.Model) llm.ModelClient { return client },
		Stdin:      strings.NewReader("/model big\n/exit\n"),
		Stdout:     out,
		Stderr:     out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := gitrepo.Trailer("Big Model"); g.CommitTrailer != want {
		t.Errorf("trailer after /model = %q, want %q", g.CommitTrailer, want)
	}
}

func TestDiffAndUndoSession(t *testing.T) {
	root := initScratchRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	g.CommitTrailer = gitrepo.Trailer("test-model")

	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = editCommitStub()
	cdr.Repo = g
	cdr.Commits = true

	// /undo before any turn: there is nothing of the session's to undo.
	input := strings.NewReader(
		"/undo\n" +
			"change the greeting\n" +
			"/diff\n" +
			"/undo\n" +
			"/exit\n")
	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		Git:        g,
		ModelAlias: "test",
		Stdin:      input,
		Stdout:     out,
		Stderr:     out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cdr.Confirm = coder.AutoConfirmer{Granted: coder.StaticGrants(map[string]bool{coder.GrantBash: true, coder.GrantSteps: true, coder.GrantContext: true}), Fallback: r.Confirmer()}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"no Strument edits to undo in this session", // pre-turn /undo
		"Commit ",                    // the commit tool's announcement
		"Use /undo to undo a commit", // undo hint after the turn
		"Diff since ",
		"-hello world", // /diff shows the change
		"+hello strument",
		"Removed 1 commit: ", // /undo
		"Now at:  ",
		"base commit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}

	// The undo restored the file and moved HEAD back to base.
	if data, _ := os.ReadFile(filepath.Join(root, "main.txt")); string(data) != "hello world\n" && string(data) != "hello world\r\n" {
		t.Errorf("file after undo = %q", data)
	}
	sha, _, subject, _, err := g.HeadInfo()
	if err != nil || subject != "base commit" {
		t.Errorf("HEAD after undo: %q %q %v", sha, subject, err)
	}
	if g.IsDirty("main.txt") {
		t.Errorf("main.txt dirty after undo")
	}
}

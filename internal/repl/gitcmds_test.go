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
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/gitrepo"
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

func editResponseStub() *fixture.StreamStub {
	response := "Sure.\n\nmain.txt\n```\n<<<<<<< SEARCH\nhello world\n=======\nhello strument\n>>>>>>> REPLACE\n```\n"
	return &fixture.StreamStub{Turns: []fixture.Turn{{Events: []fixture.Event{
		{Kind: "Answer", Text: response},
		{Kind: "Finish", FinishReason: "stop"},
	}}}}
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

	cdr := coder.New(root, small)
	cdr.Client = &fixture.StreamStub{}
	cdr.Repo = g

	cfg := testConfig(small)
	cfg.Models["big"] = big

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     cfg,
		Git:        g,
		ModelAlias: "test",
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
	g.Message = func(_, _ string) string { return "feat: greet strument" }

	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = editResponseStub()
	cdr.Repo = g
	cdr.AutoCommits = true
	cdr.AddFile("main.txt")

	// /undo before any commit: HEAD is not a session commit.
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
	cdr.Confirm = coder.AutoConfirmer{Yes: true, Fallback: r.Confirmer()}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"The last commit was not made by Strument in this chat session.", // pre-turn /undo
		"Commit ",                                // auto-commit announcement
		"You can use /undo to undo and discard ", // undo hint after the turn
		"Diff since ",
		"-hello world", // /diff shows the change
		"+hello strument",
		"Removed: ", // /undo
		"Now at:  ",
		"base commit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}

	// The undo restored the file and moved HEAD back to base.
	if data, _ := os.ReadFile(filepath.Join(root, "main.txt")); string(data) != "hello world\n" {
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

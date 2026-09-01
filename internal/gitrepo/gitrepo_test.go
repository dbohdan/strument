// Scratch-repo tests for the git port (guide phase 8 oracle): plumbing,
// the commit contract (trailer via argv, untouched author identity),
// and an end-to-end coder auto-commit integration.

package gitrepo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/gitrepo"
)

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// errOrEmpty returns the error's message, or "" for a nil error, so callers can
// grep it for environmental failure signatures without nil checks at every site.
func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initRepo creates a scratch repo with one committed file, a configured
// user, and no hooks or signing surprises.
func initRepo(t *testing.T) (root string) {
	t.Helper()
	gitOrSkip(t)
	root = t.TempDir()
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "config", "user.name", "Scratch User")
	run(t, root, "git", "config", "user.email", "scratch@example.com")
	run(t, root, "git", "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "main.txt")
	run(t, root, "git", "commit", "-q", "-m", "base commit")
	return root
}

func TestDiscoverAndPlumbing(t *testing.T) {
	root := initRepo(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	g, err := gitrepo.Discover(sub)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := filepath.EvalSymlinks(g.Root()); err != nil || got != mustEval(t, root) {
		t.Errorf("Root() = %q, want %q", g.Root(), root)
	}

	if files := g.TrackedFiles(); len(files) != 1 || files[0] != "main.txt" {
		t.Errorf("TrackedFiles = %v", files)
	}
	if !g.PathInRepo("main.txt") || g.PathInRepo("nope.txt") {
		t.Error("PathInRepo wrong")
	}
	if g.HeadSHA() == "" {
		t.Error("HeadSHA empty")
	}

	if g.IsDirty("main.txt") {
		t.Error("clean file reported dirty")
	}
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !g.IsDirty("main.txt") {
		t.Error("modified file not dirty")
	}
	// Untracked files are not dirty (GitPython semantics).
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if g.IsDirty("new.txt") {
		t.Error("untracked file reported dirty")
	}

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !g.GitIgnored("build.log") || g.GitIgnored("main.txt") {
		t.Error("GitIgnored wrong")
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDiscoverOutsideRepo(t *testing.T) {
	gitOrSkip(t)
	if _, err := gitrepo.Discover(t.TempDir()); err == nil {
		t.Error("Discover outside a repo must fail")
	}
}

func TestTrailerSanitized(t *testing.T) {
	if got := gitrepo.Trailer("deepseek/deepseek-v4-flash"); got != "Assisted-by: deepseek/deepseek-v4-flash via Strument" {
		t.Errorf("got %q", got)
	}
	if got := gitrepo.Trailer("evil\nmodel\x00\x1b[31m"); got != "Assisted-by: evilmodel[31m via Strument" {
		t.Errorf("sanitize: got %q", got)
	}
	if got := gitrepo.Trailer("\n\x01"); got != "Assisted-by: unknown-model via Strument" {
		t.Errorf("empty after sanitize: got %q", got)
	}
}

func TestCommitContract(t *testing.T) {
	root := initRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	g.CommitTrailer = gitrepo.Trailer("test-model")
	var gotDiffs, gotContext string
	g.Message = func(diffs, context string) string {
		gotDiffs, gotContext = diffs, context
		return `"feat: change the greeting"` // models love quoting; stripped
	}

	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("hello strument\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, message, ok, err := g.Commit([]string{"main.txt"}, "USER: change it", "", true)
	if err != nil || !ok {
		t.Fatalf("Commit: ok=%v err=%v", ok, err)
	}
	if hash == "" || message != "feat: change the greeting" {
		t.Errorf("hash=%q message=%q", hash, message)
	}
	if !strings.Contains(gotDiffs, "hello strument") || gotContext != "USER: change it" {
		t.Errorf("message inputs: diffs=%q context=%q", gotDiffs, gotContext)
	}

	body := run(t, root, "git", "log", "-1", "--format=%B")
	if !strings.Contains(body, "feat: change the greeting") {
		t.Errorf("commit body: %q", body)
	}
	if !strings.Contains(body, "Assisted-by: test-model via Strument") {
		t.Errorf("trailer missing: %q", body)
	}
	// author and committer identity untouched.
	ids := run(t, root, "git", "log", "-1", "--format=%an|%ae|%cn")
	if strings.TrimSpace(ids) != "Scratch User|scratch@example.com|Scratch User" {
		t.Errorf("identity overridden: %q", ids)
	}

	// Nothing staged => ok=false, no error, no commit.
	head := g.HeadSHA()
	if _, _, ok, err := g.Commit([]string{"main.txt"}, "", "", true); ok || err != nil {
		t.Errorf("no-op commit: ok=%v err=%v", ok, err)
	}
	if g.HeadSHA() != head {
		t.Error("no-op commit moved HEAD")
	}
}

// TestCommitSignFlag verifies the git_sign setting reaches `git commit` as the
// expected argv, without depending on a working gpg setup: a fake git shim logs
// the invocation and short-circuits the commit (so no real signing is attempted),
// while delegating everything else to the real git binary. Where the shim isn't
// picked up — Windows, whose PATH lookup wants a .exe/.bat, won't run a bare
// `git` script — the real git is exercised and a gpg signing failure is skipped
// rather than failed, since missing gpg is environmental, not a plumbing bug.
func TestCommitSignFlag(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}

	for _, tc := range []struct {
		name string
		sign string
		want string
	}{
		{"plain", "-S", "-S"},
		{"keyid", "-SABC123", "-SABC123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := initRepo(t)
			g, err := gitrepo.Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			g.Sign = tc.sign
			g.CommitTrailer = gitrepo.Trailer("test-model")

			if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("hello strument\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			// A shim git that logs its argv and fakes a successful commit so no
			// real gpg signing run can hang or fail the test. On Windows the PATH
			// lookup ignores a bare `git` script (it wants .exe/.bat), so the shim
			// is simply not used; in that case the real git runs and a gpg failure
			// below is skipped as environmental.
			shimDir := t.TempDir()
			logFile := filepath.Join(shimDir, "git.log")
			shim := filepath.Join(shimDir, "git")
			script := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n" +
				"if [ \"$3\" = \"commit\" ]; then exit 0; fi\n" +
				"exec \"" + realGit + "\" \"$@\"\n"
			if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			if _, _, ok, err := g.Commit([]string{"main.txt"}, "", "", true); err != nil || !ok {
				// Missing gpg (notably on Windows CI, where the shim is ignored)
				// is environmental; don't fail the plumbing check over it.
				if strings.Contains(errOrEmpty(err), "gpg") || strings.Contains(errOrEmpty(err), "GPG") {
					t.Skip("gpg signing unavailable")
				}
				t.Fatalf("Commit: ok=%v err=%v", ok, err)
			}

			log, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), " "+tc.want+" ") {
				t.Errorf("git commit argv %q not found in %q", tc.want, log)
			}
		})
	}
}

func TestUnattributedCommitHasNoTrailer(t *testing.T) {
	root := initRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	g.CommitTrailer = gitrepo.Trailer("test-model")

	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, message, ok, err := g.Commit([]string{"main.txt"}, "", "", false)
	if err != nil || !ok {
		t.Fatalf("Commit: ok=%v err=%v", ok, err)
	}
	if message != "(no commit message provided)" {
		t.Errorf("fallback message: %q", message)
	}
	if body := run(t, root, "git", "log", "-1", "--format=%B"); strings.Contains(body, "Assisted-by") {
		t.Errorf("dirty commit must not carry the trailer: %q", body)
	}
}

func TestCommitNewFile(t *testing.T) {
	root := initRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := g.Commit([]string{"created.txt"}, "", "", true); !ok || err != nil {
		t.Fatalf("new-file commit: ok=%v err=%v", ok, err)
	}
	if !g.PathInRepo("created.txt") {
		t.Error("created.txt not tracked after commit")
	}
}

// editStub replays an edit tool call against main.txt, then the closing answer
// that ends the turn. Two turns, because a reply ending in a tool call is
// mid-sentence: the harness re-sends on the result.
func editStub() *fixture.StreamStub {
	args := `{"path":"main.txt","old_string":"hello world\n","new_string":"hello strument\n"}`
	return &fixture.StreamStub{Turns: []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: "ToolCall", ToolIndex: 0, ToolID: "call_1", ToolName: "edit", ToolArgs: args},
			{Kind: "Finish", FinishReason: "tool_calls"},
		}},
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "Changed the greeting."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
	}}
}

type yesConfirmer struct{}

func (yesConfirmer) Confirm(coder.ConfirmRequest) coder.ConfirmResult {
	return coder.ConfirmResult{Yes: true}
}

type quietOutput struct{ testing.TB }

func (o quietOutput) Printf(format string, args ...any)   { o.Logf(format, args...) }
func (o quietOutput) Toolf(format string, args ...any)    { o.Logf(format, args...) }
func (o quietOutput) ToolBlock(title, _ string)           { o.Logf("%s …", title) }
func (o quietOutput) Warningf(format string, args ...any) { o.Logf(format, args...) }
func (o quietOutput) Errorf(format string, args ...any)   { o.Logf(format, args...) }
func (o quietOutput) Link(string)                         {}
func (o quietOutput) StreamText(string)                   {}
func (o quietOutput) StreamReasoning(string)              {}
func (o quietOutput) StreamToolCall(int, string, string)  {}
func (o quietOutput) FlushStream()                        {}

func newIntegrationCoder(t *testing.T, root string, g *gitrepo.Repo) *coder.Coder {
	t.Helper()
	model := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "tool",
	}
	model.SideModel = model

	c := coder.New(root, model)
	c.Client = editStub()
	c.Confirm = yesConfirmer{}
	c.Out = quietOutput{t}
	c.Repo = g
	c.AutoCommits = true
	c.AddFile("main.txt")
	return c
}

func TestCoderAutoCommitIntegration(t *testing.T) {
	root := initRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	g.CommitTrailer = gitrepo.Trailer("test-model")
	g.Message = func(_, _ string) string { return "feat: greet strument" }

	c := newIntegrationCoder(t, root, g)
	baseHead := g.HeadSHA()

	c.Run(t.Context(), "change the greeting")

	if got, _ := os.ReadFile(filepath.Join(root, "main.txt")); string(got) != "hello strument\n" {
		t.Fatalf("file = %q", got)
	}
	head := g.HeadSHA()
	if head == baseHead {
		t.Fatal("no auto-commit created")
	}
	body := run(t, root, "git", "log", "-1", "--format=%B")
	if !strings.Contains(body, "feat: greet strument") ||
		!strings.Contains(body, "Assisted-by: test-model via Strument") {
		t.Errorf("commit body: %q", body)
	}
	if c.LastCommitHash() == "" || !c.IsSessionCommit(c.LastCommitHash()) {
		t.Errorf("session commit tracking: last=%q", c.LastCommitHash())
	}
	// Worktree clean after commit.
	if g.IsDirty("main.txt") {
		t.Error("main.txt dirty after auto-commit")
	}
}

func TestDirtyCommitBeforeEdits(t *testing.T) {
	root := initRepo(t)
	g, err := gitrepo.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	g.CommitTrailer = gitrepo.Trailer("test-model")
	g.Message = func(_, _ string) string { return "feat: greet strument" }

	// User-dirty file before the model edits it.
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("hello world\nuser addition\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newIntegrationCoder(t, root, g)
	c.Run(t.Context(), "change the greeting")

	// Two commits on top of base: the unattributed dirty commit, then the
	// attributed edit commit.
	log := run(t, root, "git", "log", "--format=%s|%(trailers:key=Assisted-by,valueonly)")
	lines := strings.Split(strings.TrimSpace(log), "\n")
	joined := strings.Join(lines, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected 3+ commits, got:\n%s", joined)
	}
	if !strings.Contains(lines[0], "test-model via Strument") {
		t.Errorf("edit commit not attributed:\n%s", joined)
	}
	if strings.Contains(lines[1], "Strument") {
		t.Errorf("dirty commit must be unattributed:\n%s", joined)
	}
	// The user's addition survived in the dirty commit and the final file.
	if got, _ := os.ReadFile(filepath.Join(root, "main.txt")); string(got) != "hello strument\nuser addition\n" {
		t.Errorf("file = %q", got)
	}
}

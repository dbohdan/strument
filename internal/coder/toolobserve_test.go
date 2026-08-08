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

// TestSymlinksAreNamedAsSuch is a token-cost bug, watched live: a dotfiles
// directory where aliases.sh links to real/aliases.sh gave the model two paths
// with identical contents and no way to tell them apart, and it spent the whole
// turn deciding which one was meant instead of editing either. ls -l has named
// links this way for decades; so do these tools now.
func TestSymlinksAreNamedAsSuch(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"real/aliases.sh": "alias ll='ls -l'\n"})
	if err := os.Symlink(filepath.Join(c.Root, "real/aliases.sh"), filepath.Join(c.Root, "aliases.sh")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	listing := c.runLS(call("ls", `{}`))
	if !strings.Contains(listing, "aliases.sh -> ") {
		t.Errorf("ls does not name the link:\n%s", listing)
	}
	got := c.runRead(call("read", `{"path":"aliases.sh"}`))
	if !strings.Contains(got, "aliases.sh -> ") {
		t.Errorf("read does not name the link:\n%s", got)
	}
	// The target itself is an ordinary file and must not grow an arrow.
	if plain := c.runRead(call("read", `{"path":"real/aliases.sh"}`)); strings.Contains(plain, " -> ") {
		t.Errorf("a plain file was reported as a link:\n%s", plain)
	}
}

// TestVerifyIsQuietWhenItPasses pins the asymmetry between the two audiences.
// With verify_auto on, a green suite lands on every editing turn, and dumping
// its transcript buries the diffs the user is there to read; the model gets the
// whole thing either way, because to it a passing run's output is information.
func TestVerifyIsQuietWhenItPasses(t *testing.T) {
	c, out := observeEnv(t, nil)
	// The command computes its output rather than echoing a literal, so the
	// assertion below can tell the printed argv from the printed output.
	c.Verify = []config.VerifyCheck{{Name: "suite", Argv: []string{"sh", "-c", "echo $((6 * 7)) tests passed"}}}

	got := c.runVerify(t.Context(), call("verify", `{}`))
	if !strings.Contains(got, "42 tests passed") {
		t.Errorf("the model must still get the output:\n%s", got)
	}
	joined := strings.Join(out.lines, "\n")
	if strings.Contains(joined, "42 tests passed") {
		t.Errorf("a passing check must not print its output:\n%s", joined)
	}
	if !strings.Contains(joined, "suite passed") {
		t.Errorf("a passing check must still say so:\n%s", joined)
	}
}

// TestVerifyShowsAFailure is the other half: a failure is the one thing here
// the user has to read, so all of it reaches them.
func TestVerifyShowsAFailure(t *testing.T) {
	c, out := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{{Name: "suite", Argv: []string{"sh", "-c", "echo boom; exit 2"}}}

	c.runVerify(t.Context(), call("verify", `{}`))
	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "boom") || !strings.Contains(joined, "suite failed (exit status 2)") {
		t.Errorf("a failing check must print its verdict and output:\n%s", joined)
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

// TestAutoVerifyFiresOnlyAfterEdits pins the trigger. A turn that changed
// nothing has nothing to check, and running the suite anyway would be noise the
// user pays for.
func TestAutoVerifyFiresOnlyAfterEdits(t *testing.T) {
	c, out := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{{Name: "fails", Argv: []string{"false"}}}
	c.VerifyAuto = []string{"fails"}

	if msg, keep := c.runAutoVerify(t.Context()); keep || msg != "" {
		t.Errorf("a turn that edited nothing must not verify; got keep=%v msg=%q", keep, msg)
	}
	if strings.Contains(strings.Join(out.lines, "\n"), "automatic checks") {
		t.Error("nothing should have been announced")
	}

	c.editedSinceVerify = true
	msg, keep := c.runAutoVerify(t.Context())
	if !keep || !strings.Contains(msg, "did not pass") {
		t.Errorf("a failing check after an edit must continue the loop; got keep=%v msg=%q", keep, msg)
	}
}

// TestAutoVerifyDoesNotReAskAnUnchangedTree is the fix for something only live
// testing showed. Faced with a pre-existing failure the model answered, quite
// correctly, that the break was not its doing and it would leave it alone — and
// the harness ran the same check again, and again, until the budget ran out. An
// unchanged tree can only produce the same output, so a considered answer must
// end the turn rather than be re-asked.
func TestAutoVerifyDoesNotReAskAnUnchangedTree(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{{Name: "fails", Argv: []string{"false"}}}
	c.VerifyAuto = []string{"fails"}
	c.editedSinceVerify = true

	if _, keep := c.runAutoVerify(t.Context()); !keep {
		t.Fatal("the first failing round should continue the loop")
	}
	// The model replies in prose and edits nothing.
	if _, keep := c.runAutoVerify(t.Context()); keep {
		t.Error("a reply that edited nothing must end the turn, not re-run the same check")
	}
	if c.autoVerifies != 1 {
		t.Errorf("auto-verify rounds = %d, want 1 — the budget should be untouched", c.autoVerifies)
	}
}

// TestAutoVerifyPassingEndsTheTurn is the other half: when the checks pass the
// model is not sent anything, so the turn ends where it would have.
func TestAutoVerifyPassingEndsTheTurn(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{{Name: "ok", Argv: []string{"true"}}}
	c.VerifyAuto = []string{"ok"}
	c.editedSinceVerify = true

	if msg, keep := c.runAutoVerify(t.Context()); keep || msg != "" {
		t.Errorf("passing checks must end the turn; got keep=%v msg=%q", keep, msg)
	}
}

// TestAutoVerifyIsBounded pins the budget for the case it is actually for: a
// model that keeps editing and keeps failing. Each round edits, so the
// unchanged-tree gate never fires and only the counter stops it.
func TestAutoVerifyIsBounded(t *testing.T) {
	c, out := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{{Name: "fails", Argv: []string{"false"}}}
	c.VerifyAuto = []string{"fails"}

	rounds := 0
	for {
		c.editedSinceVerify = true // the model edited something each round
		_, keep := c.runAutoVerify(t.Context())
		if !keep {
			break
		}
		rounds++
		if rounds > maxAutoVerify+2 {
			t.Fatal("runAutoVerify never gave up")
		}
	}
	if rounds != maxAutoVerify {
		t.Errorf("auto-verify rounds = %d, want %d", rounds, maxAutoVerify)
	}
	if !strings.Contains(strings.Join(out.lines, "\n"), "without passing") {
		t.Error("the user should be told why it stopped")
	}
}

// TestAutoVerifyRunsInTheListedOrder confirms the one ordering rule: checks run
// in the order they are listed, which for verify_auto is that list's order, not
// the verify dict's.
func TestAutoVerifyRunsInTheListedOrder(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Verify = []config.VerifyCheck{
		{Name: "slow", Argv: []string{"echo", "slow ran"}},
		{Name: "fast", Argv: []string{"false"}},
	}
	c.VerifyAuto = []string{"fast", "slow"} // deliberately not the dict order
	c.editedSinceVerify = true

	msg, keep := c.runAutoVerify(t.Context())
	if !keep {
		t.Fatal("a failing check must continue the loop")
	}
	if strings.Contains(msg, "slow ran") {
		t.Errorf("the list's order was not honored; slow ran before fast:\n%s", msg)
	}
}

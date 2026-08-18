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

// A clipped line ends in "…", which on its own is just a character that might
// have been in the file. The result says once that lines were shortened, and
// where to get the rest — the same rule the empty-result and truncation notes
// follow: never let a cut pass for the whole answer.
func TestGrepSaysWhenItShortenedLines(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"fixture.jsonl": `{"blob":"` + strings.Repeat("x", 4000) + `","tag":"Target"}` + "\n",
		"short.go":      "// Target\n",
	})

	got := c.runGrep(call("grep", `{"pattern":"Target","mode":"content"}`))
	// The whole sentence, not just the word: plural() carries the count itself,
	// and passing it a second time printed "1 long 1 line was shortened". A
	// Contains check on "shortened" is exactly what missed that.
	if want := `(1 long line was shortened, marked with "…".`; !strings.Contains(got, want) {
		t.Errorf("want %q in:\n%s", want, got)
	}
	if !strings.Contains(got, "short.go:1: // Target") {
		t.Errorf("an untouched line must survive intact:\n%s", got)
	}
	if len(got) > 2000 {
		t.Errorf("a 4 KB line reached the result: %d bytes", len(got))
	}
}

// TestGrepReportsItsScope: the scope and mode shape the answer completely, so a
// report naming only the pattern points at the wrong argument. Watched live — a
// search scoped by a directory-shaped glob came back "no matches", and the next
// step widened the pattern, which was the only thing named.
func TestGrepReportsItsScope(t *testing.T) {
	c, out := observeEnv(t, map[string]string{
		"src/a.go": "package a\nfunc Target() {}\n",
		"doc.txt":  "Target\n",
	})

	c.runGrep(call("grep", `{"pattern":"Target","path":"src","glob":"**/*.go","mode":"content"}`))
	// Whitespace-free arguments print unquoted, per quoteToolArg.
	line := strings.Join(out.lines, "\n")
	for _, want := range []string{"Searched for Target", "under src", "matching **/*.go", "as content"} {
		if !strings.Contains(line, want) {
			t.Errorf("outcome line is missing %s:\n%s", want, line)
		}
	}
}

// TestGrepDistinguishesItsNothings is the whole point of the change: three
// different empty results that used to read identically, and only one of them
// means "that text is not in this project".
func TestGrepDistinguishesItsNothings(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"internal/coder/a.go": "package coder\nfunc Target() {}\n",
	})

	// A directory as a glob, and a non-recursive "*.go": both natural first
	// guesses, both admit no files at all, and neither says anything about
	// whether the pattern exists.
	for _, args := range []string{
		`{"pattern":"Target","glob":"internal/coder"}`,
		`{"pattern":"Target","glob":"*.go"}`,
	} {
		got := c.runGrep(call("grep", args))
		if !strings.Contains(got, "nothing is in that scope") {
			t.Errorf("%s should say the scope was empty:\n%s", args, got)
		}
		if !strings.Contains(got, `"**/*.go"`) {
			t.Errorf("%s should say how to write a working glob:\n%s", args, got)
		}
		// It must not claim the pattern is absent, which is the false statement
		// the old message made.
		if strings.Contains(got, "No matches for") {
			t.Errorf("%s blamed the pattern for an empty scope:\n%s", args, got)
		}
	}

	// A genuine scope with no hit says so, and says how much it looked at — which
	// is what makes it trustworthy rather than just another empty answer.
	genuine := c.runGrep(call("grep", `{"pattern":"Absent","glob":"**/*.go"}`))
	if !strings.Contains(genuine, "No matches for") || !strings.Contains(genuine, "1 file searched") {
		t.Errorf("a genuine miss should report what it searched:\n%s", genuine)
	}
}

// TestGlobExplainsAnEmptyMatch: glob has the same rules, so it owes the same
// explanation rather than letting a caller conclude the files do not exist.
func TestGlobExplainsAnEmptyMatch(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"internal/coder/a.go": "package coder\n"})
	got := c.runGlob(call("glob", `{"pattern":"*.go"}`))
	if !strings.Contains(got, "No files match") || !strings.Contains(got, `"**/*.go"`) {
		t.Errorf("glob should explain its own syntax on an empty match:\n%s", got)
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

// TestCheckRunsNamedCheck is the security-relevant one: the tool takes a name,
// looks the argv up in the config, and runs that — nothing the model sends can
// change what executes.
func TestCheckRunsNamedCheck(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Check = []config.Check{
		{Name: "ok", Argv: []string{"true"}},
		{Name: "echo", Argv: []string{"echo", "hello from check"}},
	}

	got := c.runCheckTool(t.Context(), call("check", `{"name":"echo"}`))
	if !strings.Contains(got, "hello from check") || !strings.Contains(got, "Exit status: 0") {
		t.Errorf("check result:\n%s", got)
	}

	unknown := c.runCheckTool(t.Context(), call("check", `{"name":"nope"}`))
	if !strings.Contains(unknown, "no check named") || !strings.Contains(unknown, "ok, echo") {
		t.Errorf("an unknown name must list the real ones, got:\n%s", unknown)
	}
}

// TestCheckStopsAtFirstFailure pins the ordering contract: checks run in the
// order the user declared, and a failure ends the run so the user's fast checks
// can shield the slow ones.
func TestCheckStopsAtFirstFailure(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Check = []config.Check{
		{Name: "fails", Argv: []string{"false"}},
		{Name: "never", Argv: []string{"echo", "should not run"}},
	}

	got := c.runCheckTool(t.Context(), call("check", `{}`))
	if strings.Contains(got, "should not run") {
		t.Errorf("a later check ran after a failure:\n%s", got)
	}
	if !strings.Contains(got, "Stopped here") {
		t.Errorf("the model must be told the run stopped early:\n%s", got)
	}
}

func TestCheckWithoutConfigSaysSo(t *testing.T) {
	c, _ := observeEnv(t, nil)
	if got := c.runCheckTool(t.Context(), call("check", `{}`)); !strings.Contains(got, "No checks are configured") {
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

// TestCheckIsQuietWhenItPasses pins the asymmetry between the two audiences.
// With check_auto on, a green suite lands on every editing turn, and dumping
// its transcript buries the diffs the user is there to read; the model gets the
// whole thing either way, because to it a passing run's output is information.
func TestCheckIsQuietWhenItPasses(t *testing.T) {
	c, out := observeEnv(t, nil)
	// The command computes its output rather than echoing a literal, so the
	// assertion below can tell the printed argv from the printed output.
	c.Check = []config.Check{{Name: "suite", Argv: []string{"sh", "-c", "echo $((6 * 7)) tests passed"}}}

	got := c.runCheckTool(t.Context(), call("check", `{}`))
	if !strings.Contains(got, "42 tests passed") {
		t.Errorf("the model must still get the output:\n%s", got)
	}
	joined := strings.Join(out.lines, "\n")
	if strings.Contains(joined, "42 tests passed") {
		t.Errorf("a passing check must not print its output:\n%s", joined)
	}
	if !strings.Contains(joined, "\npassed") {
		t.Errorf("a passing check must still say so:\n%s", joined)
	}
}

// TestCheckShowsAFailure is the other half: a failure is the one thing here
// the user has to read, so all of it reaches them.
func TestCheckShowsAFailure(t *testing.T) {
	c, out := observeEnv(t, nil)
	c.Check = []config.Check{{Name: "suite", Argv: []string{"sh", "-c", "echo boom; exit 2"}}}

	c.runCheckTool(t.Context(), call("check", `{}`))
	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "boom") || !strings.Contains(joined, "\nfailed (exit status 2)") {
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

// TestAutoCheckFiresOnlyAfterEdits pins the trigger. A turn that changed
// nothing has nothing to check, and running the suite anyway would be noise the
// user pays for.
func TestAutoCheckFiresOnlyAfterEdits(t *testing.T) {
	c, out := observeEnv(t, nil)
	c.Check = []config.Check{{Name: "fails", Argv: []string{"false"}}}
	c.CheckAuto = []string{"fails"}

	if msg, keep := c.runAutoCheck(t.Context()); keep || msg != "" {
		t.Errorf("a turn that edited nothing must not check; got keep=%v msg=%q", keep, msg)
	}
	if strings.Contains(strings.Join(out.lines, "\n"), "automatic checks") {
		t.Error("nothing should have been announced")
	}

	c.editedSinceCheck = true
	msg, keep := c.runAutoCheck(t.Context())
	if !keep || !strings.Contains(msg, "did not pass") {
		t.Errorf("a failing check after an edit must continue the loop; got keep=%v msg=%q", keep, msg)
	}
}

// TestAutoCheckDoesNotReAskAnUnchangedTree is the fix for something only live
// testing showed. Faced with a pre-existing failure the model answered, quite
// correctly, that the break was not its doing and it would leave it alone — and
// the harness ran the same check again, and again, until the budget ran out. An
// unchanged tree can only produce the same output, so a considered answer must
// end the turn rather than be re-asked.
func TestAutoCheckDoesNotReAskAnUnchangedTree(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Check = []config.Check{{Name: "fails", Argv: []string{"false"}}}
	c.CheckAuto = []string{"fails"}
	c.editedSinceCheck = true

	if _, keep := c.runAutoCheck(t.Context()); !keep {
		t.Fatal("the first failing round should continue the loop")
	}
	// The model replies in prose and edits nothing.
	if _, keep := c.runAutoCheck(t.Context()); keep {
		t.Error("a reply that edited nothing must end the turn, not re-run the same check")
	}
	if c.autoChecks != 1 {
		t.Errorf("auto-check rounds = %d, want 1 — the budget should be untouched", c.autoChecks)
	}
}

// TestAutoCheckPassingEndsTheTurn is the other half: when the checks pass the
// model is not sent anything, so the turn ends where it would have.
func TestAutoCheckPassingEndsTheTurn(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Check = []config.Check{{Name: "ok", Argv: []string{"true"}}}
	c.CheckAuto = []string{"ok"}
	c.editedSinceCheck = true

	if msg, keep := c.runAutoCheck(t.Context()); keep || msg != "" {
		t.Errorf("passing checks must end the turn; got keep=%v msg=%q", keep, msg)
	}
}

// TestAutoCheckIsBounded pins the budget for the case it is actually for: a
// model that keeps editing and keeps failing. Each round edits, so the
// unchanged-tree gate never fires and only the counter stops it.
func TestAutoCheckIsBounded(t *testing.T) {
	c, out := observeEnv(t, nil)
	c.Check = []config.Check{{Name: "fails", Argv: []string{"false"}}}
	c.CheckAuto = []string{"fails"}

	rounds := 0
	for {
		c.editedSinceCheck = true // the model edited something each round
		_, keep := c.runAutoCheck(t.Context())
		if !keep {
			break
		}
		rounds++
		if rounds > maxAutoCheck+2 {
			t.Fatal("runAutoCheck never gave up")
		}
	}
	if rounds != maxAutoCheck {
		t.Errorf("auto-check rounds = %d, want %d", rounds, maxAutoCheck)
	}
	if !strings.Contains(strings.Join(out.lines, "\n"), "without passing") {
		t.Error("the user should be told why it stopped")
	}
}

// TestAutoCheckRunsInTheListedOrder confirms the one ordering rule: checks run
// in the order they are listed, which for check_auto is that list's order, not
// the check dict's.
func TestAutoCheckRunsInTheListedOrder(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.Check = []config.Check{
		{Name: "slow", Argv: []string{"echo", "slow ran"}},
		{Name: "fast", Argv: []string{"false"}},
	}
	c.CheckAuto = []string{"fast", "slow"} // deliberately not the dict order
	c.editedSinceCheck = true

	msg, keep := c.runAutoCheck(t.Context())
	if !keep {
		t.Fatal("a failing check must continue the loop")
	}
	if strings.Contains(msg, "slow ran") {
		t.Errorf("the list's order was not honored; slow ran before fast:\n%s", msg)
	}
}

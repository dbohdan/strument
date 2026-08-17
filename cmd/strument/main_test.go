package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/history"
)

// newRepo makes a repository with a subdirectory and returns both paths.
func newRepo(t *testing.T) (root, sub string) {
	t.Helper()
	root = t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	// t.TempDir can hand back a symlinked path (/tmp -> /private/tmp on macOS)
	// while git reports the resolved one. The hash is taken over the string, so
	// compare against what the filesystem actually calls it.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	sub = filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, sub
}

// A transcript belongs to the project, and inside a repository the project is
// the worktree root — from any subdirectory of it.
func TestHistoryRootIsTheWorktreeRoot(t *testing.T) {
	root, sub := newRepo(t)
	for _, dir := range []string{root, sub} {
		if got := historyRootFrom(dir); got != root {
			t.Errorf("historyRootFrom(%s) = %s, want %s", dir, got, root)
		}
	}
}

// Outside a repository there is nothing to climb to, so the directory is the
// project. This is the --no-git-in-a-plain-directory case that the snapshot
// substrate exists for.
func TestHistoryRootOutsideARepoIsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if got := historyRootFrom(dir); got != dir {
		t.Errorf("historyRootFrom(%s) = %s, want itself", dir, got)
	}
}

// The regression. chat used to derive the history root honoring --no-git and
// history used to derive it ignoring --no-git, so `chat --no-git` in a
// subdirectory wrote proj/sub's transcript while `strument history` printed
// proj's — two paths, two hashes, and no way to find your own history.
//
// Both commands call historyRoot now, so the invariant to pin is that the path
// does not depend on where under the project you stand, or on --no-git, which
// says how a turn is committed rather than which project it belongs to.
func TestHistoryPathIsTheSameFromAnywhereInTheProject(t *testing.T) {
	root, sub := newRepo(t)
	cfg := &config.Config{}

	fromRoot, err := resolveHistoryPath(cfg, historyRootFrom(root))
	if err != nil {
		t.Fatal(err)
	}
	fromSub, err := resolveHistoryPath(cfg, historyRootFrom(sub))
	if err != nil {
		t.Fatal(err)
	}
	if fromRoot != fromSub {
		t.Errorf("the transcript moved with the working directory:\n root %s\n sub  %s", fromRoot, fromSub)
	}
	if filepath.Base(fromRoot) == filepath.Base(sub)+".md" {
		t.Error("the path is keyed on the subdirectory rather than the project")
	}
}

// A relative history_file override resolves against the same root, so the two
// mechanisms cannot disagree about which project this is either.
func TestHistoryOverrideResolvesAgainstTheProjectRoot(t *testing.T) {
	root, sub := newRepo(t)
	cfg := &config.Config{HistoryFile: "notes/chat.md"}

	want := filepath.Join(root, "notes", "chat.md")
	for _, dir := range []string{root, sub} {
		got, err := resolveHistoryPath(cfg, historyRootFrom(dir))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("from %s: got %s, want %s", dir, got, want)
		}
	}
}

// The alias is recorded only when it differs from the config's default.
// Recording the default would pin a project to whatever it happened to be the
// first time the project was opened, so that later editing `default` in
// config.star would mysteriously not take effect there.
func TestResumeRecordsOnlyANonDefaultAlias(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, _ := newRepo(t)
	cfg := &config.Config{Default: "mimo"}
	cdr := coder.New(root, &config.Model{Slug: "x"})

	save := saveResumeFunc(cdr, cfg, root, true)
	if save == nil {
		t.Fatal("no save function when state is kept")
	}

	save("mimo") // the default: nothing to remember
	if got := history.LoadResume(root).Model; got != "" {
		t.Errorf("the default alias was pinned: %q", got)
	}
	save("sonnet") // a deliberate choice: remember it
	if got := history.LoadResume(root).Model; got != "sonnet" {
		t.Errorf("model = %q, want sonnet", got)
	}
	// Switching back to the default is the way out of the pin.
	save("mimo")
	if got := history.LoadResume(root).Model; got != "" {
		t.Errorf("switching back to the default left %q pinned", got)
	}
}

// --no-history means leave no trace, so there is nothing to call.
func TestResumeIsNotSavedWithoutState(t *testing.T) {
	if save := saveResumeFunc(nil, nil, "/tmp/whatever", false); save != nil {
		t.Error("a no-trace session should have no save function")
	}
}

// Paths are recorded relative to the project root rather than the coder's, so
// they survive --no-git, where the coder works from the invocation directory
// while the project is still the worktree.
func TestResumePathsAreProjectRelative(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, sub := newRepo(t)
	if err := os.WriteFile(filepath.Join(sub, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A --no-git session in sub/: the coder's root is sub, the project is root.
	cdr := coder.New(sub, &config.Model{Slug: "x"})
	cdr.AddFile(filepath.Join(sub, "b.go"))

	saveResumeFunc(cdr, &config.Config{Default: "m"}, root, true)("m")

	got := history.LoadResume(root).Files
	if len(got) != 1 || got[0] != "sub/b.go" {
		t.Errorf("files = %v, want [sub/b.go]", got)
	}
}

// The REPL has always had Options.IsTerminal, and interactive() defaults to
// true when it is nil — so an unwired seam does not fail, it silently makes the
// binary believe a pipe is a terminal. Piping the output then wrote the banner,
// a full-width rule before every prompt, and the waiting line's "\r\x1b[K"
// erase into the file.
//
// That was not merely untidy. A trial scored model answers with a line anchor,
// the stray erase sequence sat at the start of the line it anchored to, and
// half the sessions read as unanswered — turning a real effect (10/12 vs 5/12)
// into a clean null (5/12 vs 4/12, p=1.0). See doc/experimenting.md.
//
// The check is on the wiring rather than on rendering, because rendering is
// already covered (internal/repl's pipe and pty tests both set IsTerminal
// explicitly, which is exactly why neither of them could catch this).
func TestTerminalDetectionIsWired(t *testing.T) {
	// go test gives the process a pipe for stdout, so this must report false.
	// If it reports true here it would report true under a shell redirect too.
	if drivingATerminal() {
		t.Error("stdout is a pipe under `go test`, so this must not claim a terminal")
	}
	// And the halves are independent: colour is a different question from
	// terminal-ness, so gating the erase on Color would leave NO_COLOR=1 users
	// staring at an unerased "Waiting for ..." line.
	if isCharDevice(os.Stdout) {
		t.Error("isCharDevice(os.Stdout) should be false under a pipe")
	}
}

// A file argument resolved through a symlinked working directory must still be
// accepted when it is genuinely inside the project — the same case /add handles
// by joining the pattern with the already-resolved coder root. The project root
// is git's symlink-resolved path while cwd can sit in the symlink namespace, so
// the containment check has to resolve both before comparing. Without that, a
// real in-project file arrived as "../../link/..." and was refused — the CLI
// and /add disagreeing about the same rule.
func TestFileInProjectThroughSymlink(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlinked alias for the real project, like a symlinked checkout or a
	// working directory reached through a symlink.
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// git reports the resolved root; the working directory uses the symlink.
	root := realDir
	cwd := filepath.Join(link, "sub")

	if !fileInProject(root, filepath.Join(cwd, "file.go")) {
		t.Error("an in-project file reached through a symlink must be accepted")
	}
	if !fileInProject(root, filepath.Join(cwd, "nested", "file.go")) {
		t.Error("a not-yet-created in-project file must be accepted")
	}
	// A sibling directory is genuinely outside, symlink or not.
	outside := filepath.Join(base, "other", "file.go")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if fileInProject(root, outside) {
		t.Error("a file in a sibling directory must still be refused")
	}
}

// AGENTS.md is the cross-tool convention for a project's standing instructions
// (agents.md), and this repository's own CLAUDE.md is a symlink to it. Strument
// pins it once and then gets out of the way.
//
// The lifecycle is the point, not the pin: an assistant that re-adds a file you
// dropped is worse than one that never offered.
func TestAgentsFileIsOfferedOnce(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, coder.AgentsFileName), []byte("# rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newCoder := func() *coder.Coder {
		return coder.New(root, &config.Model{Slug: "m", EditFormat: "tool"})
	}

	// First session: nothing recorded, so it is offered.
	c1 := newCoder()
	_, offered, _ := restoreSession(c1, root, history.Resume{})
	if !offered {
		t.Fatal("a project with AGENTS.md and no record should be offered it")
	}
	if got := c1.ChatFiles(); len(got) != 1 || got[0] != coder.AgentsFileName {
		t.Fatalf("pinned files = %v, want [%s]", got, coder.AgentsFileName)
	}

	// Second session, with the offer recorded and the user having dropped it:
	// dropped stays dropped.
	c2 := newCoder()
	_, offered, _ = restoreSession(c2, root, history.Resume{AutoPinned: []string{coder.AgentsFileName}})
	if offered {
		t.Error("the offer must not repeat once recorded")
	}
	if got := c2.ChatFiles(); len(got) != 0 {
		t.Errorf("pinned files = %v, want none — the user dropped it", got)
	}

	// And with it recorded *and* still pinned, it comes back as an ordinary
	// resume entry rather than as a fresh offer.
	c3 := newCoder()
	_, offered, _ = restoreSession(c3, root, history.Resume{
		AutoPinned: []string{coder.AgentsFileName}, Files: []string{coder.AgentsFileName},
	})
	if offered {
		t.Error("a file already in Files is a restore, not an offer")
	}
	if got := c3.ChatFiles(); len(got) != 1 {
		t.Errorf("pinned files = %v, want it restored", got)
	}
}

// A project without one is left alone. Strument never creates the file — on a
// live configuration directory, which the harness is meant to be usable on,
// writing an AGENTS.md nobody asked for would be its own small rudeness.
func TestAgentsFileIsNoticedNotCreated(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	c := coder.New(root, &config.Model{Slug: "m", EditFormat: "tool"})

	if _, offered, _ := restoreSession(c, root, history.Resume{}); offered {
		t.Error("nothing to offer in a project with no AGENTS.md")
	}
	if _, err := os.Stat(filepath.Join(root, coder.AgentsFileName)); !os.IsNotExist(err) {
		t.Error("AGENTS.md must not be created")
	}
	if got := c.ChatFiles(); len(got) != 0 {
		t.Errorf("pinned files = %v, want none", got)
	}
}

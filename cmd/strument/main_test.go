package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/history"
)

func TestShellCompletions(t *testing.T) {
	for _, shell := range []string{"bash", "fish"} {
		var out strings.Builder
		if err := writeCompletions(&out, shell); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if !strings.Contains(out.String(), "shell") || !strings.Contains(out.String(), "model-config") {
			t.Errorf("%s completion does not contain expected commands:\n%s", shell, out.String())
		}
	}
}

func TestShellCompletionsRejectUnknownShell(t *testing.T) {
	var out strings.Builder
	if err := writeCompletions(&out, "powershell"); err == nil {
		t.Fatal("unknown shell was accepted")
	}
}

// captureRun captures what runConfigSets writes to os.Stdout, which is the
// seam shared by the two config subcommands.
func captureRun(kind string) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	saved := os.Stdout
	defer func() { _ = r.Close(); _ = w.Close() }()
	os.Stdout = w
	err = runConfigSets(kind)
	_ = w.Close()
	os.Stdout = saved
	var out bytes.Buffer
	_, copyErr := io.Copy(&out, r)
	if err != nil {
		return out.String(), err
	}
	return out.String(), copyErr
}

// writeTempUserConfig drops a user config into a fresh user config directory
// and chdirs into a scratch directory, so config.Load and historyRoot see a
// project without a .strument.star. The directory is redirected through the
// environment variable os.UserConfigDir honors on this OS — XDG_CONFIG_HOME
// on Linux, $HOME on macOS, %APPDATA% on Windows — and the file lands at the
// path DefaultUserConfigPath resolves, so the test cannot drift from the
// loader's own convention on any platform.
func writeTempUserConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", dir)
	case "windows":
		t.Setenv("APPDATA", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	cfgPath, err := config.DefaultUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
}

func TestConfigModelsListsSortedAliases(t *testing.T) {
	writeTempUserConfig(t, `
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"pro": model(router, "p"), "flash": model(router, "f")}
default = "pro"
`)
	t.Setenv("OPENROUTER_API_KEY", "test-key")

	got, err := captureRun("models")
	if err != nil {
		t.Fatal(err)
	}
	want := "flash\npro\n"
	if got != want {
		t.Errorf("models output = %q, want %q", got, want)
	}

	got, err = captureRun("default")
	if err != nil {
		t.Fatal(err)
	}
	if got != "pro\n" {
		t.Errorf("default output = %q, want %q", got, "pro\n")
	}
}

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

// Every flag the CLI accepts appears in the completion scripts.
//
// The generated completions this replaced could not drift; hand-written ones
// can, and the failure is silent — a flag simply stops completing, which
// nobody reports as a bug. So the scripts are checked against the CLI itself
// rather than against a list someone remembers to update.
//
// Kong is the source of truth here: walking the parsed model reaches every
// subcommand's flags, so a new subcommand is covered the day it is added.
func TestCompletionsCoverEveryFlag(t *testing.T) {
	// Each shell spells a long option its own way, and this matcher was wrong
	// twice before it was right: first looking for "--name" in both, which
	// fish fails on every flag it does support, then accepting only fish's
	// "-l name" and not its equally valid "--long name". Both times the report
	// was drift that did not exist. A check that fires for the wrong reason is
	// not a check, and one that cries wolf gets switched off.
	needle := map[string]func(string) *regexp.Regexp{
		"bash": func(n string) *regexp.Regexp { return regexp.MustCompile(`--` + regexp.QuoteMeta(n) + `\b`) },
		// fish spells the same thing two ways, "-l name" and "--long name",
		// and this script uses both. Requiring whitespace on each side rather
		// than \b is deliberate: \b would let "-l yes" match "-l yes-shell".
		"fish": func(n string) *regexp.Regexp {
			return regexp.MustCompile(`(^|\s)(-l|--long) ` + regexp.QuoteMeta(n) + `(\s|$)`)
		},
	}
	scripts := map[string]string{"bash": bashCompletion, "fish": fishCompletion}

	var root cli
	parser, err := kong.New(&root, kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}

	var walk func(n *kong.Node)
	var flags []string
	seen := map[string]bool{}
	walk = func(n *kong.Node) {
		for _, f := range n.Flags {
			// --help and --version are shell builtins of a sort: every
			// completion offers them or the user types them blind, and
			// neither is worth failing over.
			if f.Hidden || f.Name == "help" || f.Name == "version" {
				continue
			}
			if !seen[f.Name] {
				seen[f.Name] = true
				flags = append(flags, f.Name)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(parser.Model.Node)

	if len(flags) < 10 {
		t.Fatalf("only %d flags found; the walk is not reaching the subcommands", len(flags))
	}
	for shell, script := range scripts {
		for _, name := range flags {
			if !needle[shell](name).MatchString(script) {
				t.Errorf("%s completion is missing --%s (add it to "+
					"cmd/strument/completions/strument.%s)", shell, name, shell)
			}
		}
	}
}

// TestRestoreSessionNote covers the line a resumed session opens with. It had
// no test at all, which is how three of its four branches came to say "for
// editing" — a permission /add does not grant, since any file in the project is
// editable whether or not it is pinned. Only the read-only half names anything,
// matching /ls and the startup banner.
func TestRestoreSessionNote(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "spec.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name  string
		res   history.Resume
		want  string
		empty bool
	}{
		{name: "nothing", res: history.Resume{}, empty: true},
		{
			name: "plain only",
			res:  history.Resume{Files: []string{"a.go", "b.go"}},
			want: "Restored 2 pins from your last session.",
		},
		{
			name: "read-only only",
			res:  history.Resume{ReadOnly: []string{"spec.md"}},
			want: "Restored 1 pin from your last session, read-only.",
		},
		{
			name: "both",
			res:  history.Resume{Files: []string{"a.go"}, ReadOnly: []string{"spec.md"}},
			want: "Restored 2 pins from your last session, 1 of them read-only.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdr := coder.New(root, &config.Model{Slug: "test"})
			note, _, _ := restoreSession(cdr, root, tt.res)
			if tt.empty {
				if note != "" {
					t.Errorf("note = %q, want empty", note)
				}
				return
			}
			if note != tt.want {
				t.Errorf("note = %q, want %q", note, tt.want)
			}
			if strings.Contains(note, "for editing") {
				t.Errorf("note = %q: pinning grants no editing right to report", note)
			}
		})
	}
}

// writeSkill puts a minimal valid skill at root/.strument/skills/name/SKILL.md.
func writeSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, ".strument", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	src := "---\nname: " + name + "\ndescription: Does a thing.\n---\n\nThe instructions.\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTrustWithoutAConfig. `strument trust` used to read .strument.star
// directly and hand back its os.ReadFile error, so a project with skills and
// no config failed with a bare "no such file" — for the one thing it was being
// asked to do and could have done.
func TestTrustWithoutAConfig(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()
	path := writeSkill(t, root, "release-notes")

	if err := (&trustCmd{Path: root}).Run(); err != nil {
		t.Fatalf("trust with skills and no config: %v", err)
	}

	tsPath, err := config.DefaultTrustStorePath()
	if err != nil {
		t.Fatal(err)
	}
	ts, err := config.OpenTrustStore(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ts.IsTrusted(abs, src) {
		t.Errorf("the skill was not trusted")
	}
	// Trust is over content, so editing a skill revokes it. That is the whole
	// reason the command says to re-run after every edit.
	if ts.IsTrusted(abs, append(src, '\n')) {
		t.Errorf("an edited skill stayed trusted")
	}
}

// TestTrustWithNothingToTrust keeps the error case: a directory with neither a
// config nor a skill is a user who ran the command in the wrong place, and
// silently succeeding would tell them a grant happened that did not.
func TestTrustWithNothingToTrust(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := (&trustCmd{Path: t.TempDir()}).Run(); err == nil {
		t.Errorf("trust succeeded with nothing to trust")
	}
}

// TestDiscoverSkillsGatesOnTrust is the wiring check: what main hands the coder
// is trusted only after `strument trust` has run. Discovery has its own tests;
// this one is about the two being connected, which they were not for two
// commits.
func TestDiscoverSkillsGatesOnTrust(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// So the counts below are about this project and not about whatever the
	// machine running the suite happens to have installed globally.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := t.TempDir()
	writeSkill(t, root, "release-notes")

	before := discoverSkills(root)
	if len(before) != 1 || before[0].Trusted {
		t.Fatalf("before trusting: %d skills, trusted=%v", len(before), len(before) > 0 && before[0].Trusted)
	}
	if err := (&trustCmd{Path: root}).Run(); err != nil {
		t.Fatal(err)
	}
	after := discoverSkills(root)
	if len(after) != 1 || !after[0].Trusted {
		t.Fatalf("after trusting: %d skills, trusted=%v", len(after), len(after) > 0 && after[0].Trusted)
	}
}

// TestChatNoHistoryStillSends pins the call order in chatCmd.Run: cdr.Run is
// unconditional, and only the transcript append is gated on the history
// writer existing. The crash-recording commit nested Run inside
// `if hist != nil`, which turned every `chat --no-history -m …` run into a
// silent no-op that exited 0 — no request, no output, no error. Nothing
// failed loudly: the process succeeded at doing nothing, which is why the
// wire check that caught it (running a probe through the built binary) is a
// standing rule. Here the binary is built and run against a config whose
// endpoint is a closed port: the send must happen, so the run must fail with
// a connection error rather than succeed silently.
func TestChatNoHistoryStillSends(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix listener")
	}
	// A listener we immediately close gives a port where connect fails fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener available: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	root := t.TempDir()

	// Build before redirecting HOME below. A redirected HOME moves GOPATH's
	// default module cache ($HOME/go/pkg/mod) into the temp dir, and the
	// build fills it with Go's read-only module files — which t.TempDir's
	// RemoveAll cannot unlink (macOS CI: "TempDir RemoveAll cleanup:
	// permission denied"). Linux never saw this because only darwin
	// redirects HOME.
	bin := filepath.Join(t.TempDir(), "strument")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}

	// On macOS os.UserConfigDir ignores XDG_CONFIG_HOME and uses $HOME;
	// the same pattern as writeTempUserConfig.
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", filepath.Join(root, "cfg"))
	default:
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "cfg"))
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))

	userCfgPath, err := config.DefaultUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Dir(userCfgPath)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "router = provider(adapter = \"openrouter\", base_url = \"http://" + addr + "/v1\", api_key = \"test\")\n" +
		"models = {\"m\": model(router, \"test/model\", context = 100000)}\n" +
		"default = \"m\"\n"
	if err := os.WriteFile(userCfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "chat", "--no-git", "--no-history", "--no-color", "--yes", "steps", "-m", "hello")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(root, "cfg"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
	)
	// On macOS os.UserConfigDir ignores XDG_CONFIG_HOME and uses $HOME;
	// the subprocess needs the same redirect.
	if runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "HOME="+filepath.Join(root, "cfg"))
	}
	out, err := cmd.CombinedOutput()
	combined := string(out)
	// The run must fail — the endpoint is dead — and the failure must be a
	// connection error, which proves a request was attempted. The regression
	// produced exit 0 with empty output: success at doing nothing.
	if !strings.Contains(combined, "connection refused") {
		if err != nil {
			t.Fatalf("--no-history run failed with an unexpected error: %v: %s", err, combined)
		}
		// The regressed binary exited 0 with empty output: success at doing
		// nothing. Empty output after a dead endpoint is the same bug.
		t.Errorf("--no-history run attempted no send (exit %v); output: %q", err, combined)
	}
}

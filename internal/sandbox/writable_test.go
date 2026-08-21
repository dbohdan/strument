package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The writable set is the entire security decision, so it is worth testing
// exhaustively — and it is pure, so it can be, on any OS and without a kernel.

func has(t *testing.T, paths []string, want string) bool {
	t.Helper()
	abs, _ := filepath.Abs(want)
	return slices.Contains(paths, abs)
}

func TestDefaultWritableCoversASession(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	project, state := t.TempDir(), t.TempDir()

	got := DefaultWritable(project, state, []string{"/extra"})

	for _, want := range []string{project, state, os.TempDir(), os.Getenv("XDG_CACHE_HOME"), "/extra"} {
		if !has(t, got, want) {
			t.Errorf("%s is not writable; a session needs it:\n%v", want, got)
		}
	}
}

// TestDefaultWritableKeepsGoBinReadOnly pins the case that started this: a
// writable ~/go/bin is a way to replace a binary the user runs later, which is
// the durable foothold outside the diff that the review surface cannot catch.
// pkg/ beside it must stay writable or `go build` fails.
func TestDefaultWritableKeepsGoBinReadOnly(t *testing.T) {
	gopath := t.TempDir()
	for _, sub := range []string{"pkg", "bin"} {
		if err := os.MkdirAll(filepath.Join(gopath, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOPATH", gopath)

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)

	if !has(t, got, filepath.Join(gopath, "pkg")) {
		t.Errorf("the module cache is not writable; `go build` would fail:\n%v", got)
	}
	if has(t, got, gopath) {
		t.Errorf("all of GOPATH is writable, which includes bin/:\n%v", got)
	}
	if has(t, got, filepath.Join(gopath, "bin")) {
		t.Error("GOPATH/bin is writable")
	}
}

// TestDefaultWritableHonoursMovedCaches: a machine that has relocated its
// cache is respected, rather than having the default granted and the real one
// silently denied.
func TestDefaultWritableHonoursMovedCaches(t *testing.T) {
	moved := t.TempDir()
	t.Setenv("GOCACHE", moved)

	if got := DefaultWritable(t.TempDir(), t.TempDir(), nil); !has(t, got, moved) {
		t.Errorf("a relocated GOCACHE was not granted:\n%v", got)
	}
}

// TestGitDirForAWorktree: in a worktree .git is a *file* pointing outside the
// project, and Strument commits every turn. Granting only the project root
// would make every commit fail in a way that looks like a git bug.
func TestGitDirForAWorktree(t *testing.T) {
	main := t.TempDir()
	gitdir := filepath.Join(main, ".git")
	worktreeGit := filepath.Join(gitdir, "worktrees", "feature")
	if err := os.MkdirAll(worktreeGit, 0o700); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: "+worktreeGit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := gitDir(project); got != gitdir {
		t.Errorf("gitDir = %q, want the whole %q — git writes shared refs and objects there, not just the worktree's subdirectory", got, gitdir)
	}
	if !has(t, DefaultWritable(project, t.TempDir(), nil), gitdir) {
		t.Error("a worktree's git directory is not writable; every commit would fail")
	}
}

// TestGitDirForAPlainRepository adds nothing: .git is inside the project and
// already covered by the project rule.
func TestGitDirForAPlainRepository(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := gitDir(project); got != "" {
		t.Errorf("gitDir = %q for an ordinary repository, want none", got)
	}
}

func TestGitDirIgnoresRubbish(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("not a gitdir line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gitDir(project); got != "" {
		t.Errorf("gitDir = %q for a .git file with no gitdir: line, want none", got)
	}
}

// TestDefaultWritableIsDeduped: Landlock takes the rules as given, and the same
// directory arriving twice is noise in the ruleset and in any message that
// lists the writable roots to a user.
func TestDefaultWritableIsDeduped(t *testing.T) {
	project := t.TempDir()
	got := DefaultWritable(project, project, []string{project, project + "/"})

	count := 0
	for _, p := range got {
		if abs, _ := filepath.Abs(project); p == abs {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the project appears %d times:\n%v", count, got)
	}
}

// TestDefaultWritableNeverGrantsTheWholeHome is the test that would catch the
// worst possible regression: a cache default resolving to $HOME itself gives
// away the entire home directory, which is the one thing this feature exists
// to prevent.
func TestDefaultWritableNeverGrantsTheWholeHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		t.Skip("no usable home directory here")
	}
	for _, p := range DefaultWritable(t.TempDir(), t.TempDir(), nil) {
		if p == filepath.Clean(home) {
			t.Fatalf("the whole home directory is writable, via %q", p)
		}
		if p == "/" || p == filepath.VolumeName(p)+string(filepath.Separator) {
			t.Fatalf("a filesystem root is writable, via %q", p)
		}
	}
}

// TestDefaultWritableSkipsEmptyEntries: an unset environment variable must not
// become a rule, least of all one for the current working directory.
func TestDefaultWritableSkipsEmptyEntries(t *testing.T) {
	for _, p := range DefaultWritable("", "", []string{""}) {
		if p == "" || !filepath.IsAbs(p) {
			t.Errorf("a non-absolute or empty path reached the ruleset: %q", p)
		}
	}
}

// TestTempDirIsAlwaysGranted holds on every platform, because build tools
// assume a temp directory exists and os.TempDir is how each platform answers
// that: TMPDIR on Unix, TMP/TEMP on Windows.
func TestTempDirIsAlwaysGranted(t *testing.T) {
	if got := DefaultWritable(t.TempDir(), t.TempDir(), nil); !has(t, got, os.TempDir()) {
		t.Errorf("the platform's temp directory was not granted:\n%v", got)
	}
}

// TestSlashTmpIsGrantedBesideAMovedTMPDIR: plenty of tools write to /tmp
// whatever TMPDIR says, and denying it would break them while protecting
// nothing — /tmp is world-writable already, and this policy protects the user's
// own files.
//
// Unix only. Windows has no second conventional temp location, and asking for
// "/tmp" there does not fail — filepath.Abs invents a path on the current
// drive, which is how a D:\tmp ended up in the ruleset on CI.
func TestSlashTmpIsGrantedBesideAMovedTMPDIR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /tmp on Windows; os.TempDir reads TMP/TEMP and is the only answer there")
	}
	moved := t.TempDir()
	t.Setenv("TMPDIR", moved)

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)
	if !has(t, got, moved) {
		t.Errorf("TMPDIR was not granted:\n%v", got)
	}
	if !has(t, got, "/tmp") {
		t.Errorf("/tmp was not granted alongside a relocated TMPDIR:\n%v", got)
	}
}

// TestTempDirsShape checks the list directly, because the platform assumption
// it guards is not visible in the result on the platform that has it right.
//
// filepath.Abs("/tmp") on Windows does not fail. It resolves against whatever
// the current drive is and produces a confident-looking D:\tmp, so a Unix
// constant became a rule nobody would question. CI found it; nothing on a Linux
// machine could have.
func TestTempDirsShape(t *testing.T) {
	got := tempDirs()
	if len(got) == 0 || got[0] != os.TempDir() {
		t.Fatalf("tempDirs() = %v; the platform's own answer must come first", got)
	}
	if runtime.GOOS == "windows" && len(got) != 1 {
		t.Errorf("tempDirs() = %v on Windows; there is no second conventional temp directory there", got)
	}
}

// TestFlatEcosystemOverridesAreHonoured walks the ecosystems whose variable
// names a cache directly, and checks each one reaches the writable set.
//
// The risk is dull and likely: two dozen variable names is two dozen chances
// to misspell one, and a misspelling fails silently — the default is granted,
// the relocated cache is not, and the ecosystem breaks only for the people who
// moved it.
func TestFlatEcosystemOverridesAreHonoured(t *testing.T) {
	for _, tc := range []struct{ ecosystem, env string }{
		{"generic XDG", "XDG_CACHE_HOME"},
		{"Go build cache", "GOCACHE"},
		{"Go module cache", "GOMODCACHE"},
		{"Rust rustup", "RUSTUP_HOME"},
		{"Python pip", "PIP_CACHE_DIR"},
		{"Python uv", "UV_CACHE_DIR"},
		{"npm", "npm_config_cache"},
		{"Yarn cache", "YARN_CACHE_FOLDER"},
		{"Deno cache", "DENO_DIR"},
		{"Gradle", "GRADLE_USER_HOME"},
		{".NET packages", "NUGET_PACKAGES"},
		{"PHP composer", "COMPOSER_HOME"},
		{"PHP composer cache", "COMPOSER_CACHE_DIR"},
		{"Ruby bundler", "BUNDLE_USER_HOME"},
		{"Elixir mix", "MIX_HOME"},
		{"Elixir hex", "HEX_HOME"},
		{"Crystal shards", "SHARDS_CACHE_PATH"},
		{"Haskell stack", "STACK_ROOT"},
	} {
		t.Run(tc.ecosystem, func(t *testing.T) {
			moved := t.TempDir()
			t.Setenv(tc.env, moved)
			if got := DefaultWritable(t.TempDir(), t.TempDir(), nil); !has(t, got, moved) {
				t.Errorf("%s moved to %s via %s and was not granted; check the variable's spelling",
					tc.ecosystem, moved, tc.env)
			}
		})
	}
}

// TestScannedEcosystemOverridesAreHonoured does the same for the roots that
// hold a cache beside an executable directory. Their contents are granted a
// subdirectory at a time, so the check is that a subdirectory of the relocated
// root is granted — and that the root itself is not.
func TestScannedEcosystemOverridesAreHonoured(t *testing.T) {
	for _, tc := range []struct{ ecosystem, env, sub string }{
		{"Go", "GOPATH", "pkg"},
		{"Rust cargo", "CARGO_HOME", "registry"},
		{"pnpm", "PNPM_HOME", "store"},
		{"Bun", "BUN_INSTALL", "install"},
		{"Deno installs", "DENO_INSTALL_ROOT", "shared"},
		{"Ruby gems", "GEM_HOME", "gems"},
		{"Haskell cabal", "CABAL_DIR", "packages"},
		{".NET CLI", "DOTNET_CLI_HOME", "sdk"},
	} {
		t.Run(tc.ecosystem, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(tc.env, root)
			if err := os.MkdirAll(filepath.Join(root, tc.sub), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}

			got := DefaultWritable(t.TempDir(), t.TempDir(), nil)
			if !has(t, got, filepath.Join(root, tc.sub)) {
				t.Errorf("%s/%s was not granted after %s moved:\n%v", root, tc.sub, tc.env, got)
			}
			if has(t, got, root) {
				t.Errorf("all of %s was granted, which hands over its bin/ too", tc.env)
			}
			if has(t, got, filepath.Join(root, "bin")) {
				t.Errorf("%s/bin was granted", tc.env)
			}
		})
	}
}

// TestScannedRootExcludesWhateverIsOnPath is the property the whole scan
// exists for. "Bytes that get rebuilt" and "code that will later run as me"
// are the distinction that matters and the filesystem does not record it —
// ~/go/pkg and ~/go/bin are one directory apart. PATH does record it, so the
// policy reads PATH rather than guessing from the layout.
func TestScannedRootExcludesWhateverIsOnPath(t *testing.T) {
	root := t.TempDir()
	shims := filepath.Join(root, "shims") // not called "bin"
	cache := filepath.Join(root, "cache")
	for _, d := range []string{shims, cache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOPATH", root)
	t.Setenv("PATH", shims+string(os.PathListSeparator)+"/usr/bin")

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)
	if has(t, got, shims) {
		t.Errorf("a directory on PATH was granted; anything written there runs as the user later:\n%v", got)
	}
	if !has(t, got, cache) {
		t.Errorf("a directory that is not on PATH was withheld:\n%v", got)
	}
}

// TestScannedRootSkipsSymlinks: Landlock anchors a rule to the inode a path
// resolves to, so granting a symlinked subdirectory that points at /usr would
// grant /usr. A link inside a toolchain directory is not worth that.
func TestScannedRootSkipsSymlinks(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	t.Setenv("GOPATH", root)

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)
	for _, p := range []string{link, elsewhere} {
		if has(t, got, p) {
			t.Errorf("a symlinked subdirectory was granted (%s), which grants whatever it points at:\n%v", p, got)
		}
	}
}

// TestScannedRootWithNothingToGrantIsEmpty pins the documented first-run cost:
// a toolchain that has never run has no subdirectories, so there is nothing to
// grant and its first run will fail. sandbox_write is the answer, and the
// alternative — granting the root — is the thing this all exists to avoid.
func TestScannedRootWithNothingToGrantIsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO_HOME", root)

	got := DefaultWritable(t.TempDir(), t.TempDir(), nil)
	for _, p := range []string{root, filepath.Join(root, "bin")} {
		if has(t, got, p) {
			t.Errorf("%s was granted for a toolchain that has never run", p)
		}
	}
}

package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultWritable is the set of places a session legitimately writes to.
//
// Everything else on the filesystem is readable and executable and simply
// cannot be written. The list below is therefore the entire security decision,
// and each group is here for a reason that can be checked:
//
//   - The project, because that is the work.
//   - The state directory, because the transcript, the undo spill, the resume
//     record and the lock live there, and a session that cannot write them
//     fails at the end of a turn rather than the start.
//   - The git directory when it is elsewhere, i.e. a worktree or a submodule,
//     where .git is a file pointing outside the project.
//   - A temporary directory, because build tools assume one exists.
//   - Toolchain caches, because the first `go test` of a session writes one.
//
// The cache group is where Strument deliberately parts company with Codex CLI
// and Claude Code, which grant only the working directory and a temp directory
// and leave users to discover the rest. That is defensible for a general
// assistant and wrong here: Strument's core loop is running the project's
// checks, and a sandbox whose first act is to break `go test` is a sandbox
// that gets switched off within the hour. The widening is real, and the thing
// it widens to is caches — which a model can only poison to sabotage itself.
//
// Symlinks are not resolved here on purpose. Landlock anchors a rule to the
// inode a path resolves to, so a project root that is a symlink is handled
// already; and a symlink *inside* the project pointing outward stays
// unwritable, which is the correct answer rather than an oversight.
func DefaultWritable(projectRoot, stateDir string, extra []string) []string {
	var out []string
	add := func(paths ...string) {
		for _, p := range paths {
			if p != "" {
				out = append(out, p)
			}
		}
	}

	add(projectRoot, stateDir)
	add(gitDir(projectRoot))
	add(tempDirs()...)
	add(cacheDirs()...)
	add(extra...)

	return dedupe(out)
}

// tempDirs is where scratch files may go: TMPDIR when set, and /tmp either way.
//
// os.TempDir applies the TMPDIR-else-/tmp rule, which keeps the sandbox
// agreeing with every Go program that asks the same question — and with
// envallow.go, which passes TMPDIR through to model-run commands, so the
// directory a command picks is the directory the sandbox granted.
//
// /tmp is granted even when TMPDIR points elsewhere, because a great many
// tools write to /tmp regardless of TMPDIR — shell scripts spelling
// /tmp/thing.$$ by hand, libraries with the path compiled in. This costs
// nothing that matters: /tmp is mode 1777, so every local process can already
// write there, and the integrity this policy protects is the user's own files.
// Refusing /tmp would deny nothing an attacker wants and break builds that
// hardcode it.
//
// Only where /tmp is a real convention. Windows has no such second location,
// and filepath.Abs("/tmp") there does not fail — it silently invents a path on
// whatever the current drive happens to be, so the ruleset would carry a
// D:\tmp that means nothing to anyone.
func tempDirs() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS != "windows" && os.TempDir() != "/tmp" {
		dirs = append(dirs, "/tmp")
	}
	return dirs
}

// gitDir returns the real git directory when .git is a file rather than a
// directory — a worktree or a submodule, where it holds a `gitdir:` line
// pointing somewhere outside the project. Strument commits, so that directory
// has to be writable or every turn's commit fails in a way that looks like a
// git bug.
func gitDir(projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	dot := filepath.Join(projectRoot, ".git")
	//nolint:gosec // projectRoot is the directory the user launched in, never a model-supplied path.
	info, err := os.Lstat(dot)
	if err != nil || info.IsDir() {
		// A normal repository: .git is inside the project and already covered.
		return ""
	}
	data, err := os.ReadFile(dot) //nolint:gosec // same path, same reason.
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	path, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectRoot, path)
	}
	// The worktree's own directory is under <main>/.git/worktrees/<name>, and
	// git writes to the parent too (shared refs, objects, the index of the
	// main repository). Granting the .git directory itself rather than the
	// worktree subdirectory is what makes commits work.
	if base := filepath.Dir(filepath.Dir(path)); filepath.Base(base) == ".git" {
		return base
	}
	return path
}

// executableDirs is every directory whose contents get run by name: the
// entries of PATH, plus the conventional names in case PATH grows later.
//
// This is the answer to a question the filesystem cannot express. "Bytes that
// will be rebuilt" and "code that will later execute as me" are the security
// distinction that matters, and a path carries no trace of which it is —
// ~/go/pkg and ~/go/bin are one directory apart and mean entirely different
// things. But the distinction *is* recorded, just not in the filesystem: PATH
// is precisely the list of directories whose contents get executed by name.
// So the policy reads it rather than guessing from layout.
//
// bin and sbin are excluded too, even when not currently on PATH. They cost
// nothing to withhold, and PATH is read once at startup while a user's shell
// may add to it tomorrow.
func executableDirs() map[string]bool {
	out := map[string]bool{}
	mark := func(dir string) {
		if dir == "" || !filepath.IsAbs(dir) {
			// A relative PATH entry, or an empty one meaning the working
			// directory. Neither names a stable place to protect.
			return
		}
		out[filepath.Clean(dir)] = true
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			out[resolved] = true
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		mark(dir)
	}
	return out
}

// conventionalExecutableNames are withheld regardless of PATH.
var conventionalExecutableNames = map[string]bool{"bin": true, "sbin": true}

// writableSubdirs grants a toolchain root's contents without granting the root.
//
// The tools that made this necessary put a cache and an executable directory
// side by side: ~/.cargo holds bin/ next to registry/, ~/.bun holds bin/ next
// to install/, and pnpm keeps its store *inside* the directory that is on
// PATH. Granting the root would hand over the executables; granting nothing
// breaks the build. Granting the subdirectories, minus the ones things get run
// from, is the line that separates them.
//
// Not granting the root has a second effect worth naming: pnpm's global shims
// are files directly in $PNPM_HOME, so leaving the root ungranted keeps them
// unwritable without needing to know their names.
//
// Symlinked subdirectories are skipped. Landlock anchors a rule to the inode a
// path resolves to, so granting a symlink that points at /usr would grant
// /usr — a link inside a toolchain directory is not worth that risk.
//
// A subdirectory that does not exist yet cannot be granted, so a toolchain
// that has never run once has nothing to grant and its first run fails. That
// is the documented cost; `sandbox_write` is the answer.
func writableSubdirs(root string) []string {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	onPath := executableDirs()
	var out []string
	for _, e := range entries {
		if !e.IsDir() || conventionalExecutableNames[e.Name()] {
			// IsDir is false for a symlink here: ReadDir reports the link
			// itself, which is what we want.
			continue
		}
		full := filepath.Join(root, e.Name())
		if onPath[full] {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(full); err == nil && onPath[resolved] {
			continue
		}
		out = append(out, full)
	}
	return out
}

// cacheDirs is where the toolchains put things they will rebuild.
//
// The ecosystems are exactly the ones project_checks() detects, in the same
// order, and that is the point: a project Strument offers to run checks for is
// a project whose checks must work under the sandbox. Two lists that almost
// match are worse than either — the gap shows up as one ecosystem mysteriously
// failing, months later, in someone else's session. The list and both its
// consumers are documented together under "Language support" in doc/config.md;
// adding one is "Adding an ecosystem" in doc/README.md.
//
// Each entry reads its own toolchain's environment variable before falling
// back, so a relocated cache is respected. Those variable names are largely
// the ones envallow.go already passes through to model-run commands: if a
// variable is worth telling a command about, the directory it names is worth
// letting the command write.
//
// Several toolchains need nothing named here because their default already
// sits under ~/.cache: Go's build cache, pip, uv, Deno, Yarn 1, composer's
// cache and Crystal's shards all land there. They appear only as overrides,
// for the case where someone has moved them.
//
// The roots in scannedRoots are handled differently — see writableSubdirs.
func cacheDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	under := func(rest ...string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(append([]string{home}, rest...)...)
	}
	pick := func(env, fallback string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return fallback
	}

	// Roots that hold a cache and an executable directory side by side. Their
	// contents are granted one subdirectory at a time and the root itself
	// never is, so nothing that later runs as the user becomes writable.
	var out []string
	for _, root := range []string{
		pick("GOPATH", under("go")),                         // pkg/ beside bin/
		pick("CARGO_HOME", under(".cargo")),                 // registry/, git/ beside bin/
		pick("PNPM_HOME", under(".local", "share", "pnpm")), // store/ inside the PATH dir itself
		pick("BUN_INSTALL", under(".bun")),                  // install/cache beside bin/
		pick("DENO_INSTALL_ROOT", under(".deno")),           // bin/ only
		under(".yarn"),                            // berry/cache beside bin/
		pick("GEM_HOME", under(".gem")),           // gems/ beside bin/
		pick("CABAL_DIR", under(".cabal")),        // packages/ beside bin/
		pick("DOTNET_CLI_HOME", under(".dotnet")), // tools/ is on PATH when used
	} {
		out = append(out, writableSubdirs(root)...)
	}

	return append(out, []string{
		// Everything XDG. The single biggest entry: most toolchains default
		// their cache to somewhere under here.
		pick("XDG_CACHE_HOME", under(".cache")),

		// Go.
		pick("GOCACHE", ""),
		pick("GOMODCACHE", ""),

		// Rust. rustup holds toolchains/, whose bin directories are reached
		// through the rustup shim rather than by being on PATH themselves.
		pick("RUSTUP_HOME", under(".rustup")),

		// Python.
		pick("PIP_CACHE_DIR", ""),
		pick("UV_CACHE_DIR", ""),

		// Node and the other JavaScript runtimes.
		pick("npm_config_cache", under(".npm")),
		pick("YARN_CACHE_FOLDER", ""),

		// Deno.
		pick("DENO_DIR", ""),

		// Java: Maven, then Gradle.
		under(".m2"),
		pick("GRADLE_USER_HOME", under(".gradle")),

		// .NET.
		pick("NUGET_PACKAGES", under(".nuget", "packages")),

		// PHP.
		pick("COMPOSER_HOME", under(".composer")),
		pick("COMPOSER_CACHE_DIR", ""),

		// Ruby.
		pick("BUNDLE_USER_HOME", under(".bundle")),

		// Elixir.
		pick("MIX_HOME", under(".mix")),
		pick("HEX_HOME", under(".hex")),

		// Crystal: shards installs into the project's own lib/, so only a
		// relocated cache needs naming.
		pick("SHARDS_CACHE_PATH", ""),

		// Haskell: stack, then cabal.
		pick("STACK_ROOT", under(".stack")),
	}...)
}

func dedupe(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

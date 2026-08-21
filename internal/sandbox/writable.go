package sandbox

import (
	"os"
	"path/filepath"
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
func tempDirs() []string {
	dirs := []string{os.TempDir()}
	if os.TempDir() != "/tmp" {
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

// cacheDirs is where the common toolchains put things they will rebuild.
//
// Each is read from the environment variable that toolchain honours before
// falling back to its default, so a machine that has moved its cache is
// respected. The variable names are deliberately the same ones envallow.go
// already passes through to model-run commands: if a variable is worth telling
// the command about, the directory it names is worth letting the command write.
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

	return []string{
		pick("XDG_CACHE_HOME", under(".cache")),
		pick("GOCACHE", ""),
		pick("GOMODCACHE", ""),
		// GOPATH holds bin/ as well as pkg/, and bin/ must stay read-only:
		// a writable ~/go/bin is a way to replace a binary the user will run
		// later, which is exactly the durable foothold this policy denies.
		filepath.Join(pick("GOPATH", under("go")), "pkg"),
		filepath.Join(pick("CARGO_HOME", under(".cargo")), "registry"),
		filepath.Join(pick("CARGO_HOME", under(".cargo")), "git"),
		pick("RUSTUP_HOME", under(".rustup")),
		pick("UV_CACHE_DIR", ""),
		pick("PIP_CACHE_DIR", ""),
		pick("npm_config_cache", under(".npm")),
		under(".m2"),
		pick("GRADLE_USER_HOME", under(".gradle")),
	}
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

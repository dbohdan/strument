package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"go.starlark.net/starlark"
)

// detector recognizes one ecosystem in a project root and says what to run for
// it.
//
// checks returns names already prefixed with the ecosystem — "go-test", not
// "test". Prefixing always, rather than only on collision, means a polyglot
// repository never silently loses one language's suite to another's, and the
// names are visible to the model in the check tool's description anyway.
type detector struct {
	// name is for the doc comment's sake and for tests to point at.
	name   string
	checks func(root string) []Check
}

// detectors run in this order, and each ecosystem lists its own checks fast to
// slow, because a bare check() runs them in order and stops at the first
// failure. The order is fixed rather than derived so that two runs over one
// directory produce byte-identical config.
//
// Two rules decide what may appear here, and both exist to keep the list
// defensible rather than merely long:
//
//  1. Only commands the marker's own toolchain ships, or ones the project's own
//     configuration names. `go vet` and `go test` come with Go; pytest does not
//     come with Python, so it appears only when a pyproject.toml, pytest.ini, or
//     tox.ini declares it. This answers "but is that tool even installed?" once,
//     structurally, instead of per row.
//  2. Where the command runs a *user-defined* target — make, task, just, npm,
//     composer — the marker alone is not enough. A Makefile without a test rule
//     would yield `make test`, which is not a check but a confusing failure. The
//     target has to be there.
//
// What this list is NOT: commands that cannot do harm. Every one of them
// executes the project's own code — `npm test` runs whatever package.json says,
// `make test` runs the Makefile, `cargo test` compiles and runs the crate. No
// test runner is safe in that sense and claiming otherwise here would be the
// most dangerous sentence in this file. What they are is commands whose effect
// is determined by the project's own committed configuration, which the user
// already trusts by opening the project. That trust is why project_checks() is
// opt-in: it takes effect because someone wrote it in a config, never because a
// file happened to be on disk.
var detectors = []detector{
	{"go", func(root string) []Check {
		if !exists(root, "go.mod") {
			return nil
		}
		return []Check{
			{Name: "go-vet", Argv: []string{"go", "vet", "./..."}},
			{Name: "go-test", Argv: []string{"go", "test", "./..."}},
		}
	}},

	{"rust", func(root string) []Check {
		if !exists(root, "Cargo.toml") {
			return nil
		}
		// cargo check and cargo test ship with cargo; clippy is a rustup
		// component that may not be installed, so it stays out under rule 1.
		return []Check{
			{Name: "cargo-check", Argv: []string{"cargo", "check"}},
			{Name: "cargo-test", Argv: []string{"cargo", "test"}},
		}
	}},

	{"python", func(root string) []Check {
		// The project's own config has to name pytest; Python ships no test
		// runner that a bare `pytest` would be.
		declared := fileContains(root, "pyproject.toml", "[tool.pytest.ini_options]") ||
			exists(root, "pytest.ini") ||
			fileContains(root, "tox.ini", "[pytest]") ||
			fileContains(root, "setup.cfg", "[tool:pytest]")
		if !declared {
			return nil
		}
		return []Check{{Name: "py-test", Argv: []string{"pytest"}}}
	}},

	{"node", func(root string) []Check {
		if !hasJSONScript(root, "package.json", "test") {
			return nil
		}
		// The lockfile picks the package manager. "bun run test" rather than
		// "bun test": the latter is Bun's own test runner and deliberately
		// ignores the package.json script, so it would run something else.
		argv := []string{"npm", "test"}
		switch {
		case exists(root, "pnpm-lock.yaml"):
			argv = []string{"pnpm", "test"}
		case exists(root, "yarn.lock"):
			argv = []string{"yarn", "test"}
		case exists(root, "bun.lock") || exists(root, "bun.lockb"):
			argv = []string{"bun", "run", "test"}
		}
		return []Check{{Name: "node-test", Argv: argv}}
	}},

	{"deno", func(root string) []Check {
		if !exists(root, "deno.json") && !exists(root, "deno.jsonc") {
			return nil
		}
		// Both take the project from deno.json when given no arguments.
		return []Check{
			{Name: "deno-check", Argv: []string{"deno", "check"}},
			{Name: "deno-test", Argv: []string{"deno", "test"}},
		}
	}},

	{"make", func(root string) []Check {
		return targetChecks(root, "make", makefileTarget,
			[]string{"Makefile", "makefile", "GNUmakefile"},
			[]string{"lint", "check", "test"})
	}},

	{"task", func(root string) []Check {
		return targetChecks(root, "task", yamlKey,
			[]string{"Taskfile.yml", "Taskfile.yaml", "Taskfile.dist.yml", "Taskfile.dist.yaml"},
			[]string{"lint", "test"})
	}},

	{"just", func(root string) []Check {
		return targetChecks(root, "just", justRecipe,
			[]string{"justfile", "Justfile", ".justfile"},
			[]string{"lint", "test"})
	}},

	{"maven", func(root string) []Check {
		if !exists(root, "pom.xml") {
			return nil
		}
		return []Check{{Name: "mvn-test", Argv: []string{"mvn", "test"}}}
	}},

	{"gradle", func(root string) []Check {
		// The wrapper only. A bare `gradle` may not be installed, and the
		// wrapper is the form a Gradle project is meant to be built with.
		if runtime.GOOS == "windows" {
			if exists(root, "gradlew.bat") {
				return []Check{{Name: "gradle-test", Argv: []string{"gradlew.bat", "test"}}}
			}
			return nil
		}
		if exists(root, "gradlew") {
			return []Check{{Name: "gradle-test", Argv: []string{"./gradlew", "test"}}}
		}
		return nil
	}},

	{"dotnet", func(root string) []Check {
		if !globExists(root, "*.sln") && !globExists(root, "*.csproj") && !globExists(root, "*.slnx") {
			return nil
		}
		return []Check{
			{Name: "dotnet-build", Argv: []string{"dotnet", "build"}},
			{Name: "dotnet-test", Argv: []string{"dotnet", "test"}},
		}
	}},

	{"php", func(root string) []Check {
		if !hasJSONScript(root, "composer.json", "test") {
			return nil
		}
		return []Check{{Name: "php-test", Argv: []string{"composer", "test"}}}
	}},

	{"ruby", func(root string) []Check {
		// rspec has to be in the bundle for `bundle exec rspec` to resolve.
		// There is deliberately no rake row: telling whether a Rakefile defines
		// a test task needs `rake -T`, which means running the project's own
		// Ruby at config load, and no detection is worth that.
		if !isDir(root, "spec") || !fileContains(root, "Gemfile", "rspec") {
			return nil
		}
		return []Check{{Name: "rspec", Argv: []string{"bundle", "exec", "rspec"}}}
	}},

	{"elixir", func(root string) []Check {
		if !exists(root, "mix.exs") {
			return nil
		}
		return []Check{
			{Name: "mix-compile", Argv: []string{"mix", "compile"}},
			{Name: "mix-test", Argv: []string{"mix", "test"}},
		}
	}},

	{"crystal", func(root string) []Check {
		if !exists(root, "shard.yml") || !isDir(root, "spec") {
			return nil
		}
		return []Check{{Name: "crystal-spec", Argv: []string{"crystal", "spec"}}}
	}},

	{"haskell", func(root string) []Check {
		if exists(root, "stack.yaml") {
			return []Check{{Name: "stack-test", Argv: []string{"stack", "test"}}}
		}
		if !exists(root, "cabal.project") && !globExists(root, "*.cabal") {
			return nil
		}
		return []Check{
			{Name: "cabal-build", Argv: []string{"cabal", "build"}},
			{Name: "cabal-test", Argv: []string{"cabal", "test"}},
		}
	}},
}

// ProjectChecks returns the checks detected in root, in a fixed order. An empty
// root — no project this session — detects nothing.
func ProjectChecks(root string) []Check {
	if root == "" {
		return nil
	}
	var out []Check
	for _, d := range detectors {
		out = append(out, d.checks(root)...)
	}
	return out
}

// builtinProjectChecks is project_checks(), closed over the project root the
// same way builtinEnv is closed over the environment lookup. Both are impure by
// nature — one reads the environment, this one reads the filesystem — and both
// take that dependency as an argument so a test can supply its own.
//
// The root is the *project's*, not the config file's, which is deliberate: a
// user-level config may write `check = project_checks()` once and get
// per-project detection everywhere it opens.
func builtinProjectChecks(root string) *starlark.Builtin {
	return starlark.NewBuiltin("project_checks", func(
		_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple,
	) (starlark.Value, error) {
		if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
			return nil, err
		}
		// A dict, so it drops straight into `check` and extends with the
		// documented dict(...) idiom rather than needing a syntax of its own.
		dict := starlark.NewDict(0)
		for _, ch := range ProjectChecks(root) {
			argv := make([]starlark.Value, 0, len(ch.Argv))
			for _, word := range ch.Argv {
				argv = append(argv, starlark.String(word))
			}
			if err := dict.SetKey(starlark.String(ch.Name), starlark.NewList(argv)); err != nil {
				return nil, err
			}
		}
		return dict, nil
	})
}

// --- marker helpers ---

func exists(root, name string) bool {
	st, err := os.Stat(filepath.Join(root, name))
	return err == nil && !st.IsDir()
}

func isDir(root, name string) bool {
	st, err := os.Stat(filepath.Join(root, name))
	return err == nil && st.IsDir()
}

func globExists(root, pattern string) bool {
	matches, err := filepath.Glob(filepath.Join(root, pattern))
	return err == nil && len(matches) > 0
}

// readMarker reads a marker file, capped: these are configuration files, and a
// multi-megabyte one is not something to pull into memory to substring-search.
func readMarker(root, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || len(data) > 1<<20 {
		return "", false
	}
	return string(data), true
}

func fileContains(root, name, needle string) bool {
	body, ok := readMarker(root, name)
	return ok && strings.Contains(body, needle)
}

// hasJSONScript reports whether a package.json-shaped file defines a named
// script. Parsed rather than substring-matched: "test" appears in a dependency
// name often enough that a substring would be a coin flip.
func hasJSONScript(root, name, script string) bool {
	body, ok := readMarker(root, name)
	if !ok {
		return false
	}
	var doc struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return false
	}
	return strings.TrimSpace(doc.Scripts[script]) != ""
}

// targetChecks emits `<tool> <target>` for each target the first present marker
// file actually defines, in the order the targets are listed.
func targetChecks(root, tool string, defines func(body, target string) bool, markers, targets []string) []Check {
	var body string
	for _, m := range markers {
		if b, ok := readMarker(root, m); ok {
			body = b
			break
		}
	}
	if body == "" {
		return nil
	}
	var out []Check
	for _, target := range targets {
		if defines(body, target) {
			out = append(out, Check{Name: tool + "-" + target, Argv: []string{tool, target}})
		}
	}
	return out
}

// The three target matchers are anchored and deliberately simple. Each may miss
// a target — a Makefile that builds rule names from a variable, a Taskfile that
// includes another file — and a miss costs only a prompt the user did not need,
// the same fail-closed direction the allowlist takes. What they must not do is
// claim a target that is not there, since that turns a detected check into a
// command that fails for a reason having nothing to do with the code.
const (
	// A rule, not an assignment: `test:` and `lint test: build` match, `test :=
	// x` does not. [^=] also matches the newline of a target with no
	// prerequisites.
	makefileTargetPat = `(?m)^([A-Za-z0-9_./-]+[ \t]+)*%s[ \t]*:[^=]`
	// Indented under the top-level tasks: key, rather than any indented key of
	// that name anywhere in the file — a `test:` inside vars: or env: is not a
	// task, and running it would fail confusingly.
	taskTargetPat = `(?ms)^tasks:.*?^[ \t]+%s[ \t]*:`
	// A recipe line: the name at column zero, optional parameters, then the
	// colon. Excluding `:=` keeps just's assignments out.
	justRecipePat = `(?m)^%s\b[^:\n]*:[^=]`
)

func makefileTarget(body, target string) bool { return matchTarget(makefileTargetPat, body, target) }
func yamlKey(body, target string) bool        { return matchTarget(taskTargetPat, body, target) }
func justRecipe(body, target string) bool     { return matchTarget(justRecipePat, body, target) }

// matchTarget fills a pattern with the target's name, escaped. The names are
// ours today; a regexp built by interpolating a caller's string raw is the kind
// of thing that stops being ours later.
func matchTarget(pattern, body, target string) bool {
	re, err := regexp.Compile(strings.Replace(pattern, "%s", regexp.QuoteMeta(target), 1))
	if err != nil {
		return false
	}
	return re.MatchString(body)
}

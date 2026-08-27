package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// projectWith writes the given files (path -> contents) into a temp dir and
// returns it. A path ending in "/" is made as a directory.
func projectWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func checkNamesOf(checks []Check) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.Name)
	}
	return out
}

// TestProjectChecksDetection covers each ecosystem's marker, and — more
// importantly — the cases where a marker is present but the thing it would run
// is not. A detected check that fails because the target does not exist is
// worse than no check at all: it fails for a reason having nothing to do with
// the code.
func TestProjectChecksDetection(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  []string
	}{
		"go": {
			map[string]string{"go.mod": "module x\n"},
			[]string{"go-vet", "go-test"},
		},
		"rust": {
			map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"},
			[]string{"cargo-check", "cargo-test"},
		},

		// Python ships no test runner, so the project's own config has to name
		// one. A bare pyproject.toml is not enough.
		"python declares pytest": {
			map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\naddopts = \"-q\"\n"},
			[]string{"py-test"},
		},
		"python without pytest": {
			map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n"},
			nil,
		},
		// The runner variants are gated on the project's own lockfile: a
		// uv.lock means the project uses uv, a poetry.lock means poetry.
		"python with uv.lock": {
			map[string]string{
				"pyproject.toml": "[tool.pytest.ini_options]\naddopts = \"-q\"\n",
				"uv.lock":        "",
			},
			[]string{"py-test", "py-test-uv"},
		},
		"python with poetry.lock": {
			map[string]string{
				"pyproject.toml": "[tool.pytest.ini_options]\naddopts = \"-q\"\n",
				"poetry.lock":    "",
			},
			[]string{"py-test", "py-test-poetry"},
		},
		"python with both lockfiles": {
			map[string]string{
				"pyproject.toml": "[tool.pytest.ini_options]\naddopts = \"-q\"\n",
				"uv.lock":        "",
				"poetry.lock":    "",
			},
			[]string{"py-test", "py-test-uv", "py-test-poetry"},
		},

		"node npm": {
			map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`},
			[]string{"node-test"},
		},
		"node without a test script": {
			map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`},
			nil,
		},
		// "test" appears inside a dependency name often enough that a substring
		// scan would be a coin flip; the JSON is parsed instead.
		"node test only in a dependency name": {
			map[string]string{"package.json": `{"dependencies":{"testing-library":"1.0.0"}}`},
			nil,
		},

		"deno": {
			map[string]string{"deno.json": "{}"},
			[]string{"deno-check", "deno-test"},
		},

		"make with a test rule": {
			map[string]string{"Makefile": "all:\n\techo hi\n\ntest:\n\tgo test ./...\n"},
			[]string{"make-test"},
		},
		"make with lint and test": {
			map[string]string{"Makefile": "lint:\n\tvet\n\ntest:\n\trun\n"},
			[]string{"make-lint", "make-test"},
		},
		"make without a test rule": {
			map[string]string{"Makefile": "all:\n\techo hi\n"},
			nil,
		},
		// A variable assignment is not a rule.
		"make with a test variable": {
			map[string]string{"Makefile": "test := foo\nall:\n\techo hi\n"},
			nil,
		},

		"task": {
			map[string]string{"Taskfile.yml": "version: '3'\n\ntasks:\n  test:\n    cmds:\n      - go test\n"},
			[]string{"task-test"},
		},
		// A `test:` under vars: is not a task, and `task test` would fail.
		"task with test only in vars": {
			map[string]string{"Taskfile.yml": "version: '3'\nvars:\n  test: nope\n"},
			nil,
		},

		"just": {
			map[string]string{"justfile": "test:\n    go test ./...\n"},
			[]string{"just-test"},
		},
		"just with parameters": {
			map[string]string{"justfile": "test pkg='./...':\n    go test {{pkg}}\n"},
			[]string{"just-test"},
		},

		"maven":  {map[string]string{"pom.xml": "<project/>"}, []string{"mvn-test"}},
		"dotnet": {map[string]string{"App.csproj": "<Project/>"}, []string{"dotnet-build", "dotnet-test"}},

		"php": {
			map[string]string{"composer.json": `{"scripts":{"test":"phpunit"}}`},
			[]string{"php-test"},
		},
		"php without a test script": {
			map[string]string{"composer.json": `{"name":"x/y"}`},
			nil,
		},

		"ruby rspec": {
			map[string]string{"Gemfile": "gem 'rspec'\n", "spec/": ""},
			[]string{"rspec"},
		},
		"ruby without rspec in the bundle": {
			map[string]string{"Gemfile": "gem 'rails'\n", "spec/": ""},
			nil,
		},
		"ruby without a spec directory": {
			map[string]string{"Gemfile": "gem 'rspec'\n"},
			nil,
		},

		"elixir": {map[string]string{"mix.exs": "defmodule X do\nend\n"}, []string{"mix-compile", "mix-test"}},
		"crystal": {
			map[string]string{"shard.yml": "name: x\n", "spec/": ""},
			[]string{"crystal-spec"},
		},
		"haskell cabal": {
			map[string]string{"x.cabal": "name: x\n"},
			[]string{"cabal-build", "cabal-test"},
		},
		// stack.yaml wins outright rather than adding to cabal's row.
		"haskell stack": {
			map[string]string{"stack.yaml": "resolver: lts\n", "x.cabal": "name: x\n"},
			[]string{"stack-test"},
		},

		"swift": {
			map[string]string{"Package.swift": "// swift-tools-version:5.9\n"},
			[]string{"swift-build", "swift-test"},
		},
		"gleam": {
			map[string]string{"gleam.toml": "name = \"x\"\n"},
			[]string{"gleam-check", "gleam-test"},
		},
		"ocaml dune": {
			map[string]string{"dune-project": "(lang dune 3.0)\n"},
			[]string{"dune-build", "dune-test"},
		},
		"d dub.json": {
			map[string]string{"dub.json": "{\"name\":\"x\"}\n"},
			[]string{"dub-test"},
		},
		"d dub.sdl": {
			map[string]string{"dub.sdl": "name \"x\"\n"},
			[]string{"dub-test"},
		},
		"dart": {
			map[string]string{"pubspec.yaml": "name: x\ndev_dependencies:\n  test: ^1.0.0\n", "test/": ""},
			[]string{"dart-test"},
		},
		// A Flutter package needs flutter test; dart test cannot load the
		// bindings.
		"dart flutter": {
			map[string]string{
				"pubspec.yaml": "name: x\ndependencies:\n  flutter:\n    sdk: flutter\n",
				"test/":        "",
			},
			[]string{"flutter-test"},
		},
		// flutter_lints is a lint package, not the SDK. The colon lands after
		// the underscore, so the marker must not match it.
		"dart with flutter_lints only": {
			map[string]string{
				"pubspec.yaml": "name: x\ndev_dependencies:\n  flutter_lints: ^3.0.0\n",
				"test/":        "",
			},
			[]string{"dart-test"},
		},
		// dart test on a package with no tests is an error, not a pass.
		"dart without a test directory": {
			map[string]string{"pubspec.yaml": "name: x\n"},
			nil,
		},
		"racket": {
			map[string]string{"info.rkt": "#lang info\n"},
			[]string{"raco-test"},
		},
		// A pile of .rkt files is not a package, and raco test . over it would
		// run whatever each file does at load time.
		"racket without info.rkt": {
			map[string]string{"main.rkt": "#lang racket\n"},
			nil,
		},
		"clojure leiningen": {
			map[string]string{"project.clj": "(defproject x \"0.1\")\n"},
			[]string{"lein-test"},
		},
		// deps.edn has no conventional test alias; see the detector.
		"clojure deps.edn only": {
			map[string]string{"deps.edn": "{:paths [\"src\"]}\n"},
			nil,
		},
		"solidity foundry": {
			map[string]string{"foundry.toml": "[profile.default]\n"},
			[]string{"forge-build", "forge-test"},
		},
		"lua busted": {
			map[string]string{".busted": "return {}\n"},
			[]string{"busted"},
		},
		"lua rockspec with a test section": {
			map[string]string{"x-1.0-1.rockspec": "package = \"x\"\ntest = {\n  type = \"busted\"\n}\n"},
			[]string{"luarocks-test"},
		},
		// test_dependencies is not a test section. A substring match would
		// offer luarocks test to a project that has nothing for it to read.
		"lua rockspec without one": {
			map[string]string{"x-1.0-1.rockspec": "package = \"x\"\ntest_dependencies = { \"busted\" }\n"},
			nil,
		},
		// LuaRocks reads .busted itself, so offering both would run one suite
		// twice under two names.
		"lua busted beside a rockspec": {
			map[string]string{
				".busted":          "return {}\n",
				"x-1.0-1.rockspec": "package = \"x\"\ntest = { type = \"busted\" }\n",
			},
			[]string{"busted"},
		},
		"lua linters": {
			map[string]string{".luacheckrc": "std = \"lua54\"\n", "selene.toml": "std = \"lua51\"\n"},
			[]string{"luacheck", "selene"},
		},
		"bats in test": {
			map[string]string{"test/cli.bats": "@test \"x\" { true; }\n"},
			[]string{"bats"},
		},
		"bats in tests": {
			map[string]string{"tests/cli.bats": "@test \"x\" { true; }\n"},
			[]string{"bats"},
		},
		"elisp eldev": {
			map[string]string{"Eldev": "(eldev-use-package-archive 'melpa)\n"},
			[]string{"eldev-test"},
		},
		// A Cask file names no runnable target without evaluating Lisp.
		"elisp cask only": {
			map[string]string{"Cask": "(source melpa)\n"},
			nil,
		},
		"r testthat": {
			map[string]string{"DESCRIPTION": "Package: x\n", "tests/testthat/": ""},
			[]string{"r-test"},
		},
		// DESCRIPTION alone says this is a package, not that it has tests.
		"r without testthat": {
			map[string]string{"DESCRIPTION": "Package: x\n"},
			nil,
		},

		"mise section tasks": {
			map[string]string{"mise.toml": "[tasks.lint]\nrun = \"x\"\n\n[tasks.test]\nrun = \"y\"\n"},
			[]string{"mise-lint", "mise-test"},
		},
		"mise inline tasks": {
			map[string]string{"mise.toml": "[tasks]\nlint = \"x\"\ntest = \"y\"\n"},
			[]string{"mise-lint", "mise-test"},
		},
		"mise in .config": {
			map[string]string{".config/mise/config.toml": "[tasks.test]\nrun = \"y\"\n"},
			[]string{"mise-test"},
		},
		// A tool version or an environment variable is not a task. Running one
		// would fail for a reason having nothing to do with the code.
		"mise tools and env are not tasks": {
			map[string]string{"mise.toml": "[tools]\ntest = \"1.0\"\n\n[env]\nlint = \"x\"\n"},
			nil,
		},
		// A task below an array value. The first version of the matcher was a
		// bracket-bounded regexp, which ended at the [ of ["build"] and made
		// every task after it invisible; RE2 has no lookahead to say "up to the
		// next section header", so the matcher is a scan.
		"mise task after an array value": {
			map[string]string{"mise.toml": "[tasks]\nbuild = \"x\"\ndepends = [\"build\"]\ntest = \"y\"\n"},
			[]string{"mise-test"},
		},
		// [tasks.foo] closes the plain [tasks] table, so a key under it is that
		// task's field, not a task of its own.
		"mise key under a task section is not a task": {
			map[string]string{"mise.toml": "[tasks.build]\nrun = \"x\"\ntest = \"not a task\"\n"},
			nil,
		},

		"nothing at all": {map[string]string{"README.md": "hi\n"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := checkNamesOf(ProjectChecks(projectWith(t, tc.files)))
			if !slices.Equal(got, tc.want) {
				t.Errorf("detected %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProjectChecksIsDeterministic: the dict's insertion order is its run
// order, so two loads over one directory must not disagree about it.
func TestProjectChecksIsDeterministic(t *testing.T) {
	root := projectWith(t, map[string]string{
		"go.mod":       "module x\n",
		"Makefile":     "test:\n\tgo test\n",
		"package.json": `{"scripts":{"test":"vitest"}}`,
	})
	first := checkNamesOf(ProjectChecks(root))
	// A polyglot repository keeps both suites, which is what prefixing buys.
	want := []string{"go-vet", "go-test", "node-test", "make-test"}
	if !slices.Equal(first, want) {
		t.Fatalf("detected %v, want %v", first, want)
	}
	for range 5 {
		if got := checkNamesOf(ProjectChecks(root)); !slices.Equal(got, first) {
			t.Fatalf("order changed between calls: %v then %v", first, got)
		}
	}
}

// TestProjectChecksLockfileSelectsThePackageManager. "bun run test" rather than
// "bun test": the latter is Bun's own runner and deliberately ignores the
// package.json script, so it would run something other than the project's
// tests.
func TestProjectChecksLockfileSelectsThePackageManager(t *testing.T) {
	for lockfile, want := range map[string][]string{
		"":                  {"npm", "test"},
		"package-lock.json": {"npm", "test"},
		"pnpm-lock.yaml":    {"pnpm", "test"},
		"yarn.lock":         {"yarn", "test"},
		"bun.lock":          {"bun", "run", "test"},
	} {
		t.Run(lockfile, func(t *testing.T) {
			files := map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`}
			if lockfile != "" {
				files[lockfile] = ""
			}
			checks := ProjectChecks(projectWith(t, files))
			if len(checks) != 1 || !slices.Equal(checks[0].Argv, want) {
				t.Errorf("detected %+v, want argv %v", checks, want)
			}
		})
	}
}

// TestProjectChecksWithoutARootDetectsNothing: no project this session means
// nothing to detect, rather than scanning whatever the process's cwd happens to
// be.
func TestProjectChecksWithoutARootDetectsNothing(t *testing.T) {
	if got := ProjectChecks(""); got != nil {
		t.Errorf("detected %v with no root", checkNamesOf(got))
	}
}

// TestPythonRunnerVariantsPrependTheRunner: the variants wrap the same argv the
// base check runs, so "uv run" lands before pytest rather than somewhere that
// would change what pytest is told to do.
func TestPythonRunnerVariantsPrependTheRunner(t *testing.T) {
	root := projectWith(t, map[string]string{
		"pyproject.toml": "[tool.pytest.ini_options]\naddopts = \"-q\"\n",
		"uv.lock":        "",
		"poetry.lock":    "",
	})
	got := map[string][]string{}
	for _, c := range ProjectChecks(root) {
		got[c.Name] = c.Argv
	}
	for name, want := range map[string][]string{
		"py-test":        {"pytest"},
		"py-test-uv":     {"uv", "run", "pytest"},
		"py-test-poetry": {"poetry", "run", "pytest"},
	} {
		if !slices.Equal(got[name], want) {
			t.Errorf("%s argv = %v, want %v", name, got[name], want)
		}
	}
}

// TestProjectChecksBuiltinReturnsADict exercises it as configs use it —
// `check = project_checks()` — through the real loader, including the dict()
// idiom the documentation gives for extending it.
func TestProjectChecksBuiltinReturnsADict(t *testing.T) {
	root := projectWith(t, map[string]string{"go.mod": "module x\n"})
	cfgDir := t.TempDir()
	userPath := filepath.Join(cfgDir, "config.star")
	src := `
p = provider(adapter = "openrouter", api_key = "k")
models = {"m": model(p, "a/b")}
default = "m"
check = dict(project_checks(), lint = ["golangci-lint", "run"])
check_auto = ["go-vet"]
`
	if err := os.WriteFile(userPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{
		UserConfigPath: userPath,
		ProjectRoot:    root,
		TrustStorePath: filepath.Join(cfgDir, "trust.json"),
		LookupEnv:      func(string) (string, bool) { return "", false },
		Warn:           func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := checkNamesOf(cfg.Check)
	for _, want := range []string{"go-vet", "go-test", "lint"} {
		if !slices.Contains(got, want) {
			t.Errorf("check = %v, want it to contain %q", got, want)
		}
	}
	// check_auto validates its names against the merged check dict, so a
	// detected name having reached it is the end-to-end proof.
	if !slices.Equal(cfg.CheckAuto, []string{"go-vet"}) {
		t.Errorf("check_auto = %v", cfg.CheckAuto)
	}
}

// TestGradleNeedsTheWrapper: a bare `gradle` may not be installed, so only the
// wrapper counts, and only the one this platform can run.
func TestGradleNeedsTheWrapper(t *testing.T) {
	if got := ProjectChecks(projectWith(t, map[string]string{"build.gradle": "plugins {}\n"})); got != nil {
		t.Errorf("detected %v without a wrapper", checkNamesOf(got))
	}
	wrapper := "gradlew"
	if runtime.GOOS == "windows" {
		wrapper = "gradlew.bat"
	}
	got := ProjectChecks(projectWith(t, map[string]string{"build.gradle": "", wrapper: ""}))
	if !slices.Equal(checkNamesOf(got), []string{"gradle-test"}) {
		t.Errorf("detected %v with a wrapper present", checkNamesOf(got))
	}
}

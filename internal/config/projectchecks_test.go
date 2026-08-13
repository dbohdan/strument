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

func checkNamesOf(checks []VerifyCheck) []string {
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

// TestProjectChecksBuiltinReturnsADict exercises it as configs use it —
// `verify = project_checks()` — through the real loader, including the dict()
// idiom the documentation gives for extending it.
func TestProjectChecksBuiltinReturnsADict(t *testing.T) {
	root := projectWith(t, map[string]string{"go.mod": "module x\n"})
	cfgDir := t.TempDir()
	userPath := filepath.Join(cfgDir, "config.star")
	src := `
p = provider(adapter = "openrouter", api_key = "k")
models = {"m": model(p, "a/b")}
default = "m"
verify = dict(project_checks(), lint = ["golangci-lint", "run"])
verify_auto = ["go-vet"]
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

	got := checkNamesOf(cfg.Verify)
	for _, want := range []string{"go-vet", "go-test", "lint"} {
		if !slices.Contains(got, want) {
			t.Errorf("verify = %v, want it to contain %q", got, want)
		}
	}
	// verify_auto validates its names against the merged verify dict, so a
	// detected name having reached it is the end-to-end proof.
	if !slices.Equal(cfg.VerifyAuto, []string{"go-vet"}) {
		t.Errorf("verify_auto = %v", cfg.VerifyAuto)
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

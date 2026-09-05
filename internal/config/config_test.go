package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/multiformats/go-multihash"
)

const userConfig = `
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))

flash = model(router, "deepseek/deepseek-v4-flash", reasoning = "low", reasoning_tag = "think")

models = {
    "flash": flash,
    "pro": model(router, "deepseek/deepseek-v4-pro", side_model = "flash", temperature = 0.2),
}
default = "flash"
`

func envWith(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

// harness writes a user config (and optionally a project config) into temp
// dirs and returns ready-to-use Options.
func harness(t *testing.T, userSrc, projectSrc string, env map[string]string) Options {
	t.Helper()
	dir := t.TempDir()
	userPath := filepath.Join(dir, "config.star")
	if err := os.WriteFile(userPath, []byte(userSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		UserConfigPath: userPath,
		TrustStorePath: filepath.Join(dir, "trust"),
		LookupEnv:      envWith(env),
		Warn:           func(format string, args ...any) { t.Logf("warn: "+format, args...) },
	}
	if projectSrc != "" {
		projRoot := filepath.Join(dir, "project")
		if err := os.MkdirAll(projRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projRoot, ProjectConfigName), []byte(projectSrc), 0o600); err != nil {
			t.Fatal(err)
		}
		opts.ProjectRoot = projRoot
	}
	return opts
}

var testEnv = map[string]string{"OPENROUTER_API_KEY": "test-key-not-real"}

// TestEnvAllowParsing covers the shape rules: a plain list of names passes,
// and anything that is not a name fails the load rather than being passed
// through to a command's environment.
func TestEnvAllowParsing(t *testing.T) {
	t.Run("list of names", func(t *testing.T) {
		cfg, err := Load(harness(t, userConfig+"\nenv_allow = [\"FOO\", \"BAR\"]\n", "", testEnv))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(cfg.EnvAllow, ",") != "FOO,BAR" {
			t.Errorf("EnvAllow = %v", cfg.EnvAllow)
		}
	})

	t.Run("not a list", func(t *testing.T) {
		if _, err := Load(harness(t, userConfig+"\nenv_allow = \"FOO\"\n", "", testEnv)); err == nil ||
			!strings.Contains(err.Error(), "env_allow") {
			t.Errorf("a string env_allow should fail: %v", err)
		}
	})

	t.Run("non-string element", func(t *testing.T) {
		if _, err := Load(harness(t, userConfig+"\nenv_allow = [1]\n", "", testEnv)); err == nil ||
			!strings.Contains(err.Error(), "env_allow") {
			t.Errorf("an int env_allow entry should fail: %v", err)
		}
	})

	// "FOO=bar" as an entry is the interesting rejection: it would otherwise
	// smuggle a value through the setting, when the setting's whole design is
	// that values come from the real environment and the config carries only
	// names.
	t.Run("name with a value", func(t *testing.T) {
		if _, err := Load(harness(t, userConfig+"\nenv_allow = [\"FOO=bar\"]\n", "", testEnv)); err == nil ||
			!strings.Contains(err.Error(), "env_allow") {
			t.Errorf("a NAME=VALUE env_allow entry should fail: %v", err)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		if _, err := Load(harness(t, userConfig+"\nenv_allow = [\"\"]\n", "", testEnv)); err == nil ||
			!strings.Contains(err.Error(), "env_allow") {
			t.Errorf("an empty env_allow entry should fail: %v", err)
		}
	})
}

// TestEnvAllowProjectWins: the project file's env_allow replaces the user's
// whole-value, for the same reason check_auto does — the project must be able
// to narrow what the user's config widened, and merging could only widen.
func TestEnvAllowProjectWins(t *testing.T) {
	user := userConfig + "\nenv_allow = [\"FOO\", \"BAR\"]\n"
	proj := "env_allow = [\"BAZ\"]\n"
	opts := harness(t, user, proj, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.EnvAllow, ",") != "BAZ" {
		t.Errorf("project env_allow should replace the user's, got %v", cfg.EnvAllow)
	}
}

func TestLoadUserConfig(t *testing.T) {
	cfg, err := Load(harness(t, userConfig, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "flash" || len(cfg.Models) != 2 {
		t.Fatalf("cfg = %+v", cfg)
	}
	flash := cfg.Models["flash"]
	if flash.Slug != "deepseek/deepseek-v4-flash" || flash.Reasoning != "low" || flash.ReasoningTag != "think" {
		t.Errorf("flash = %+v", flash)
	}
	if flash.Provider.Adapter != "openrouter" || flash.Provider.APIKey != "test-key-not-real" {
		t.Errorf("provider = %+v", flash.Provider)
	}
	if !flash.RepoMap || flash.EditFormat != "tool" {
		t.Errorf("defaults wrong: %+v", flash)
	}
	// side_model resolution: string ref and None->self.
	pro := cfg.Models["pro"]
	if pro.SideModel != flash {
		t.Error("pro.SideModel should be flash")
	}
	if flash.SideModel != flash {
		t.Error("flash.SideModel should be itself (None => self)")
	}
	if pro.Temperature == nil || *pro.Temperature != 0.2 {
		t.Errorf("pro.Temperature = %v", pro.Temperature)
	}
}

func TestHistoryFileOptionalAndOverridable(t *testing.T) {
	// Absent by default.
	cfg, err := Load(harness(t, userConfig, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryFile != "" {
		t.Errorf("history_file should default to empty, got %q", cfg.HistoryFile)
	}

	// Set in the user config; a trusted project overrides it
	// (project-over-user).
	userSrc := userConfig + "\nhistory_file = \"user.md\"\n"
	projSrc := "history_file = \"project.md\"\n"
	opts := harness(t, userSrc, projSrc, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryFile != "project.md" {
		t.Errorf("history_file = %q, want project override", cfg.HistoryFile)
	}

	// A non-string history_file is rejected.
	if _, err := Load(harness(t, userConfig+"\nhistory_file = 42\n", "", testEnv)); err == nil ||
		!strings.Contains(err.Error(), "history_file") {
		t.Errorf("non-string history_file should fail: %v", err)
	}
}

// TestGitSignParsing covers the three shapes of `git_sign`: a boolean for a
// plain -S, a key-id string for -S<keyid>, and an explicit false/empty string
// for unsigned. false must override (not silently merge with) a user setting,
// because the project file wins and the user's `git_sign = True` must be
// turned off, not combined with it.
func TestGitSignParsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
		want string
	}{
		{"absent", "", ""},
		{"true", "git_sign = True", "-S"},
		{"false", "git_sign = False", ""},
		{"keyid", `git_sign = "ABC123"`, "-SABC123"},
		{"empty string", `git_sign = ""`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := userConfig + "\n" + tc.expr + "\n"
			cfg, err := Load(harness(t, src, "", testEnv))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.GitSign != tc.want {
				t.Errorf("GitSign = %q, want %q", cfg.GitSign, tc.want)
			}
		})
	}

	// A project can turn a signed user config off.
	user := userConfig + "\ngit_sign = True\n"
	proj := "git_sign = False\n"
	opts := harness(t, user, proj, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitSign != "" {
		t.Errorf("project git_sign = False should win, got %q", cfg.GitSign)
	}

	// A non-boolean, non-string value is rejected.
	if _, err := Load(harness(t, userConfig+"\ngit_sign = 7\n", "", testEnv)); err == nil ||
		!strings.Contains(err.Error(), "git_sign") {
		t.Errorf("non-bool/string git_sign should fail: %v", err)
	}
}

func TestAskEditFormatRejectedInConfig(t *testing.T) {
	// "ask" is a runtime-only format; a config that sets it must fail.
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "slug", edit_format = "ask")}
default = "m"
`
	_, err := Load(harness(t, src, "", testEnv))
	if err == nil || !strings.Contains(err.Error(), "edit_format") {
		t.Fatalf("edit_format=ask should be rejected, got err=%v", err)
	}
}

func TestEnvRequiredUnsetFailsLoad(t *testing.T) {
	_, err := Load(harness(t, userConfig, "", nil))
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("err = %v", err)
	}
}

// TestEnvDefaultMakesItOptional is the whole of env's contract, which used to
// take two parameters to express and got it wrong in the case anyone would
// write: env("X", default = "y") errored on a missing X, because a separate
// required defaulted to True and short-circuited before the default was
// reached. Giving a default is now what makes a variable optional.
//
// The shape is Starlark's own dictionary access, so a config author already
// knows it: env("X") is d["x"] and raises when the key is absent,
// env("X", default = v) is d.get("x", v) and does not.
func TestEnvDefaultMakesItOptional(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expr    string
		want    string // the resolved api_key
		wantErr string // non-empty => loading must fail saying this
	}{
		{"set, no default", `env("PRESENT")`, "from-env", ""},
		{"set, with default", `env("PRESENT", default = "unused")`, "from-env", ""},
		{"unset, no default", `env("MISSING")`, "", "MISSING"},
		{"unset, with default", `env("MISSING", default = "fallback")`, "fallback", ""},
		{"unset, positional default", `env("MISSING", "positional")`, "positional", ""},
		{"unset, empty default", `env("MISSING", default = "")`, "", ""},
		// required is gone. UnpackArgs rejects it by name, which says enough.
		{"required is no longer a thing", `env("MISSING", required = False)`, "", "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(`
p = provider("openai", api_key = %s)
models = {"m": model(p, "gpt")}
default = "m"
`, tc.expr)
			cfg, err := Load(harness(t, src, "", map[string]string{"PRESENT": "from-env"}))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want an error mentioning %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Models["m"].Provider.APIKey; got != tc.want {
				t.Errorf("api key = %q, want %q", got, tc.want)
			}
		})
	}
}

// The keyword's presence is what counts, not its value: default = None means
// optional-with-no-value, which omitting the keyword does not. Checked on its
// own because provider() wants a string api_key and would reject the None
// before env's behavior could be seen.
func TestEnvExplicitNoneIsOptional(t *testing.T) {
	src := `
maybe = env("MISSING", default = None)
p = provider("openai", api_key = "k", base_url = "https://example.invalid")
models = {"m": model(p, "gpt")}
default = "m"
`
	if _, err := Load(harness(t, src, "", nil)); err != nil {
		t.Fatalf("default = None should make the variable optional: %v", err)
	}
}

// The error for a missing variable has to say what to do about it: the reader
// is looking at a name they may have spelled wrong, or at a variable they
// meant to be optional, and those need different fixes.
func TestEnvMissingErrorSuggestsTheDefault(t *testing.T) {
	src := `
p = provider("openai", api_key = env("NOPE"))
models = {"m": model(p, "gpt")}
default = "m"
`
	_, err := Load(harness(t, src, "", nil))
	if err == nil {
		t.Fatal("a missing variable with no default must fail the load")
	}
	for _, want := range []string{`"NOPE"`, "default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestUntrustedProjectConfigIsInert(t *testing.T) {
	project := `
p = provider("openai", api_key = "attacker")
models = {"evil": model(p, "evil-model")}
default = "evil"
`
	var warned []string
	opts := harness(t, userConfig, project, testEnv)
	opts.Warn = func(format string, _ ...any) { warned = append(warned, format) }
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Models["evil"]; ok {
		t.Error("untrusted project models reached the host")
	}
	if cfg.Default != "flash" {
		t.Errorf("default = %q", cfg.Default)
	}
	if len(warned) == 0 {
		t.Error("no warning about the untrusted config")
	}
}

func TestTrustedProjectConfigMergesAndWins(t *testing.T) {
	project := `
p = provider("openai", base_url = "https://proxy.corp/v1", api_key = "corp")
models = {
    "flash": model(p, "corp-flash"),
    "extra": model(p, "corp-extra", side_model = "pro"),
}
default = "extra"
`
	opts := harness(t, userConfig, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Whole-key override: project's flash replaces the user's entirely.
	if cfg.Models["flash"].Slug != "corp-flash" || cfg.Models["flash"].Provider.Adapter != "openai" {
		t.Errorf("flash = %+v", cfg.Models["flash"])
	}
	// Project default wins.
	if cfg.Default != "extra" {
		t.Errorf("default = %q", cfg.Default)
	}
	// Cross-file side_model: project model references the user's alias
	// (resolution is post-merge).
	if cfg.Models["extra"].SideModel != cfg.Models["pro"] {
		t.Error("extra.SideModel should resolve to the user's pro model")
	}
}

func TestEditingProjectConfigRearmsGate(t *testing.T) {
	project := "models = {}\n"
	opts := harness(t, userConfig, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	// Edit the file after trusting.
	projPath := filepath.Join(opts.ProjectRoot, ProjectConfigName)
	if err := os.WriteFile(projPath, []byte("default = \"pwned\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned bool
	opts.Warn = func(string, ...any) { warned = true }
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "flash" {
		t.Errorf("edited project config was honored: default = %q", cfg.Default)
	}
	if !warned {
		t.Error("no warning after gate re-armed")
	}
}

func TestMultihashSelfDescription(t *testing.T) {
	dir := t.TempDir()
	ts, err := OpenTrustStore(filepath.Join(dir, "trust"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("models = {}\n")
	// A record written under a non-default function (simulating an old
	// default) must keep verifying under its own algorithm.
	if err := ts.TrustWithCode("/proj/.strument.star", content, multihash.SHA2_512); err != nil {
		t.Fatal(err)
	}
	ts2, err := OpenTrustStore(filepath.Join(dir, "trust"))
	if err != nil {
		t.Fatal(err)
	}
	if !ts2.IsTrusted("/proj/.strument.star", content) {
		t.Error("sha2-512 record failed to verify after reload")
	}
	if ts2.IsTrusted("/proj/.strument.star", []byte("models = {}  # edited\n")) {
		t.Error("digest did not change on content edit")
	}
	if ts2.IsTrusted("/other/.strument.star", content) {
		t.Error("unrecorded path trusted")
	}
}

func TestProviderValueSemanticsGrouping(t *testing.T) {
	src := `
p1 = provider("openrouter", api_key = "k")
p2 = provider("openrouter", api_key = "k")
p3 = provider("openrouter", base_url = "https://other.example/v1", api_key = "k")
models = {
    "a": model(p1, "s1"),
    "b": model(p2, "s2"),
    "c": model(p3, "s3"),
}
default = "a"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, m := range cfg.Models {
		groups[m.Provider.GroupKey()] = true
	}
	if len(groups) != 2 {
		t.Errorf("want 2 endpoint groups, got %d", len(groups))
	}
}

func TestExtraParamsFencing(t *testing.T) {
	// Reserved transport key rejected.
	src := `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s", extra_params = {"stream": False})}
default = "m"
`
	if _, err := Load(harness(t, src, "", nil)); err == nil || !strings.Contains(err.Error(), "reserved transport key") {
		t.Errorf("err = %v", err)
	}

	// Non-JSON value rejected.
	src2 := `
def f(): pass
p = provider("openai", api_key = "k")
models = {"m": model(p, "s", extra_params = {"hook": f})}
default = "m"
`
	if _, err := Load(harness(t, src2, "", nil)); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("err = %v", err)
	}

	// Passthrough merges model over provider.
	src3 := `
p = provider("openrouter", api_key = "k", extra_params = {"service_tier": "default", "seed": 1})
models = {"m": model(p, "s", extra_params = {"seed": 2})}
default = "m"
`
	cfg, err := Load(harness(t, src3, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	params := cfg.Models["m"].RequestExtraParams()
	if params["service_tier"] != "default" {
		t.Errorf("provider param lost: %v", params)
	}
	if params["seed"] != int64(2) {
		t.Errorf("model param should win: %v", params["seed"])
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"empty models", "models = {}\ndefault = \"x\"\n", "no models"},
		{"default not in models", `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s")}
default = "nope"
`, "not a key"},
		{"unknown edit format", `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s", edit_format = "udiff")}
default = "m"
`, "edit_format"},
		{"unknown adapter", `
p = provider("bedrock", api_key = "k")
models = {"m": model(p, "s")}
default = "m"
`, "unknown adapter"},
		{"missing side alias", `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s", side_model = "ghost")}
default = "m"
`, "ghost"},
		// weak_model was renamed to side_model, and is still recognized so a
		// config using it gets a sentence naming the new name rather than
		// "unexpected keyword argument", which is true and useless. The old
		// name made a claim about capability, and claims about model
		// capability go stale: the model that usually sits here now is a
		// near-peer of a frontier one.
		{"renamed weak_model", `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s", weak_model = "m")}
default = "m"
`, "side_model"},
	}
	for _, c := range cases {
		_, err := Load(harness(t, c.src, "", nil))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v (want containing %q)", c.name, err, c.wantErr)
		}
	}
}

// Starlark gives config a real language: derive a fleet of models from one
// provider with a comprehension.
func TestStarlarkComprehension(t *testing.T) {
	src := `
p = provider("openrouter", api_key = "k")
slugs = {"flash": "deepseek/deepseek-v4-flash", "pro": "deepseek/deepseek-v4-pro"}
models = {alias: model(p, slug, reasoning = "low") for alias, slug in slugs.items()}
default = "flash"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 2 || cfg.Models["pro"].Reasoning != "low" {
		t.Errorf("cfg = %+v", cfg.Models)
	}
}

func TestSideModelInlineValue(t *testing.T) {
	src := `
p = provider("openai", api_key = "k")
cheap = model(p, "cheap-slug")
models = {"main": model(p, "main-slug", side_model = cheap)}
default = "main"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	main := cfg.Models["main"]
	if main.SideModel == nil || main.SideModel.Slug != "cheap-slug" {
		t.Fatalf("side = %+v", main.SideModel)
	}
	// The inline side model is its own side model.
	if main.SideModel.SideModel != main.SideModel {
		t.Error("inline side model should self-resolve")
	}
}

func TestModelWithExtraParamsMethod(t *testing.T) {
	// Overrides merge over existing params and win; a model with no params
	// starts from an empty dict. The receiver is left unchanged.
	src := `
p = provider("openrouter", api_key = "k")
base = model(p, "slug", extra_params = {"seed": 1, "service_tier": "default"})
models = {
    "base": base,
    "overridden": base.with_extra_params(seed = 2, extra = True),
    "plain": model(p, "slug2").with_extra_params(thinking = "on"),
}
default = "overridden"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}

	baseParams := cfg.Models["base"].RequestExtraParams()
	if len(baseParams) != 2 || baseParams["seed"] != int64(1) {
		t.Errorf("the receiver must be unchanged: %v", baseParams)
	}

	over := cfg.Models["overridden"]
	if over.Slug != "slug" {
		t.Errorf("the copy must keep the model's fields: slug = %q", over.Slug)
	}
	overParams := over.RequestExtraParams()
	if overParams["seed"] != int64(2) {
		t.Errorf("override should win: %v", overParams["seed"])
	}
	if overParams["service_tier"] != "default" {
		t.Errorf("existing param lost: %v", overParams)
	}
	if overParams["extra"] != true {
		t.Errorf("new param missing: %v", overParams)
	}

	plain := cfg.Models["plain"].RequestExtraParams()
	if len(plain) != 1 || plain["thinking"] != "on" {
		t.Errorf("plain = %v", plain)
	}

	// Reserved transport keys are fenced through the method, too.
	bad := `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s").with_extra_params(stream = False)}
default = "m"
`
	if _, err := Load(harness(t, bad, "", nil)); err == nil ||
		!strings.Contains(err.Error(), "reserved transport key") {
		t.Errorf("reserved key should fail: %v", err)
	}

	// Positional arguments are rejected.
	pos := `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s").with_extra_params({"seed": 1})}
default = "m"
`
	if _, err := Load(harness(t, pos, "", nil)); err == nil ||
		!strings.Contains(err.Error(), "keyword") {
		t.Errorf("positional args should fail: %v", err)
	}
}

// TestCheckParsesInDeclaredOrder pins the two properties the check tool
// depends on: a dict of name -> argv, kept in the order it was written, because
// a bare check run stops at the first failure and fast checks belong first.
func TestCheckParsesInDeclaredOrder(t *testing.T) {
	src := userConfig + `
check = {
    "lint": ["golangci-lint", "run"],
    "test": ["go", "test", "./..."],
    "build": ["go", "build", "./..."],
}
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lint", "test", "build"}
	if got := cfg.CheckNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("CheckNames() = %v, want %v (declaration order is meaningful)", got, want)
	}
	if got := cfg.Check[1].Argv; strings.Join(got, " ") != "go test ./..." {
		t.Errorf("check[test] argv = %v", got)
	}
}

// TestCheckProjectReplacesWholesale pins the semantics `check` shares with
// every other key a project can set: the project's value wins entire.
//
// It used to merge per key, which read as a convenience and was one — until a
// user config saying `check = project_checks()` met a project that wanted
// fewer checks, not more. Merging can override a name and add a name; it has
// no way to remove one, so there was no way to say "not that one" short of
// changing the global config for every project.
func TestCheckProjectReplacesWholesale(t *testing.T) {
	user := userConfig + `
check = {
    "lint": ["user-lint"],
    "test": ["user-test"],
}
`
	project := `
check = {
    "test": ["project-test", "-race"],
    "typecheck": ["project-tsc"],
}
`
	opts := harness(t, user, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}

	// "lint" is gone: the user set it, the project did not restate it, and a
	// project that does not name a check does not have it. That is the whole
	// change, and it is what makes dropping one possible.
	want := []string{"test", "typecheck"}
	if got := cfg.CheckNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("CheckNames() = %v, want %v", got, want)
	}
	// Order is the project's declaration order, not a splice into the user's:
	// with nothing inherited there is no other order it could be.
	if got := strings.Join(cfg.Check[0].Argv, " "); got != "project-test -race" {
		t.Errorf("first check = %q, want the project's first", got)
	}
	if got := strings.Join(cfg.Check[1].Argv, " "); got != "project-tsc" {
		t.Errorf("second check = %q, want the project's second", got)
	}
}

func TestCheckRejectsBadShapes(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"not a dict", `check = ["go", "test"]`},
		{"argv not a list", `check = {"test": "go test"}`},
		{"argv element not a string", `check = {"test": ["go", 1]}`},
		{"empty argv", `check = {"test": []}`},
		{"empty name", `check = {"  ": ["go"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(harness(t, userConfig+"\n"+tc.src+"\n", "", testEnv)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// TestCheckAutoValidatesAgainstMergedChecks pins the property that makes the
// feature trustworthy: a name that isn't a real check fails at load. A typo
// here would otherwise mean the harness silently runs nothing, which is the
// one failure mode automatic checking exists to prevent.
func TestCheckAutoValidatesAgainstMergedChecks(t *testing.T) {
	good := userConfig + `
check = {"lint": ["golangci-lint", "run"], "test": ["go", "test", "./..."]}
check_auto = ["lint", "test"]
`
	cfg, err := Load(harness(t, good, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.CheckAuto, ",") != "lint,test" {
		t.Errorf("CheckAuto = %v", cfg.CheckAuto)
	}

	bad := userConfig + `
check = {"test": ["go", "test", "./..."]}
check_auto = ["lnit"]
`
	_, err = Load(harness(t, bad, "", testEnv))
	if err == nil {
		t.Fatal("a check_auto name with no matching check must fail at load")
	}
	if !strings.Contains(err.Error(), "lnit") || !strings.Contains(err.Error(), "test") {
		t.Errorf("error should name the typo and the real checks, got: %v", err)
	}
}

// TestCheckAutoValidatesAfterTheProjectMerge covers why validation is
// post-merge: the project can supply the check the user's check_auto names.
func TestCheckAutoValidatesAfterTheProjectMerge(t *testing.T) {
	user := userConfig + "\ncheck_auto = [\"typecheck\"]\n"
	project := `check = {"typecheck": ["tsc", "--noEmit"]}`
	opts := harness(t, user, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(opts); err != nil {
		t.Errorf("a project-supplied check should satisfy the user's check_auto: %v", err)
	}
}

// Every adapter name provider() accepts must survive config load. "anthropic"
// used to be rejected as reserved; the case that asserted so is gone from the
// error table above, and this replaces it.
func TestEveryAdapterLoads(t *testing.T) {
	for _, adapter := range []string{
		AdapterOpenAI, AdapterOpenRouter, AdapterOpenCode,
		AdapterAnthropic, AdapterOpenCodeAnthropic,
	} {
		src := `
p = provider("` + adapter + `", api_key = "k")
models = {"m": model(p, "s")}
default = "m"
`
		cfg, err := Load(harness(t, src, "", nil))
		if err != nil {
			t.Errorf("adapter %q should load: %v", adapter, err)
			continue
		}
		if got := cfg.Models["m"].Provider.Adapter; got != adapter {
			t.Errorf("adapter = %q, want %q", got, adapter)
		}
	}
}

// The opencode adapter has to survive config load like any other, and keep
// value semantics so two identical providers still group onto one client.
func TestOpenCodeAdapterLoads(t *testing.T) {
	src := `
oc = provider("opencode", api_key = "k")
models = {"mimo": model(oc, "mimo-v2.5", context = 1050000)}
default = "mimo"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatalf("opencode provider should load: %v", err)
	}
	m := cfg.Models["mimo"]
	if got := m.Provider.Adapter; got != AdapterOpenCode {
		t.Errorf("adapter = %q, want %q", got, AdapterOpenCode)
	}
	// No base_url: the client supplies the Go subscription's endpoint.
	if got := m.Provider.BaseURL; got != "" {
		t.Errorf("base_url = %q, want it left to the adapter default", got)
	}
	// opencode's own config writes "opencode-go/mimo-v2.5"; the endpoint takes
	// the bare id, so the slug must pass through untouched.
	if got := m.Slug; got != "mimo-v2.5" {
		t.Errorf("slug = %q, want it sent verbatim", got)
	}
}

// `shell = False` is a standing decision about a project, so it has to survive
// config load like any other global, and reject a non-boolean rather than
// quietly meaning something.
func TestShellSetting(t *testing.T) {
	load := func(src string) (*Config, error) { return Load(harness(t, src, "", nil)) }
	base := `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s")}
default = "m"
`
	cfg, err := load(base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if cfg.NoShell {
		t.Error("shell is off by default; it must be on unless a config says otherwise")
	}

	cfg, err = load(base + "shell = False\n")
	if err != nil {
		t.Fatalf("shell = False: %v", err)
	}
	if !cfg.NoShell {
		t.Error("`shell = False` did not disable the shell")
	}

	if cfg, err = load(base + "shell = True\n"); err != nil || cfg.NoShell {
		t.Errorf("`shell = True` should be the default state: err=%v NoShell=%v", err, cfg.NoShell)
	}

	if _, err = load(base + `shell = "no"` + "\n"); err == nil ||
		!strings.Contains(err.Error(), "`shell` must be a boolean") {
		t.Errorf("a non-boolean shell should be rejected by name, got %v", err)
	}
}

// A project config disabling the shell must win over a user config that leaves
// it alone, the same way the other project-level globals do.
func TestProjectShellOverridesUser(t *testing.T) {
	user := `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s")}
default = "m"
`
	opts := harness(t, user, "shell = False\n", nil)
	// A project config only speaks once it is trusted; without this the test
	// would pass or fail for the wrong reason.
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.NoShell {
		t.Error("a project config's `shell = False` was ignored")
	}
}

// The idiom that replaces merging. A project that wants the standard checks
// plus one of its own restates them by calling project_checks() itself — one
// call, and the file then says what it does without reference to a user config
// the reader cannot see.
//
// This is also the form the documentation used to get wrong. It showed
// `check = dict(check, ...)` under a .strument.star heading, which fails with
// "global variable check referenced before assignment": a project config has
// never been able to see the user's `check`, so that example only ever worked
// inside a single file.
func TestProjectExtendsProjectChecks(t *testing.T) {
	user := userConfig + "\ncheck = {\"lint\": [\"user-lint\"]}\n"
	project := `check = dict(project_checks(), extra = ["echo", "hi"])`

	opts := harness(t, user, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatalf("the documented extend idiom must load: %v", err)
	}
	names := cfg.CheckNames()
	if !slices.Contains(names, "extra") {
		t.Errorf("checks = %v, want the project's own check present", names)
	}
	if slices.Contains(names, "lint") {
		t.Errorf("checks = %v, want the user's lint gone — the project did not restate it", names)
	}
}

// And the form that does not work, held where it is so the documentation
// cannot drift back to it: a project config cannot read the user's `check`.
func TestProjectCannotReadTheUsersCheck(t *testing.T) {
	user := userConfig + "\ncheck = {\"lint\": [\"user-lint\"]}\n"
	project := `check = dict(check, extra = ["echo", "hi"])`

	opts := harness(t, user, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	_, err := Load(opts)
	if err == nil {
		t.Fatal("a project referring to the user's check should fail, not silently inherit it")
	}
	if !strings.Contains(err.Error(), "check referenced before assignment") {
		t.Errorf("err = %v, want it to name the unassigned global", err)
	}
}

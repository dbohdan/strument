package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multiformats/go-multihash"
)

const userConfig = `
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))

flash = model(router, "deepseek/deepseek-v4-flash", reasoning = "low", reasoning_tag = "think")

models = {
    "flash": flash,
    "pro": model(router, "deepseek/deepseek-v4-pro", weak_model = "flash", temperature = 0.2),
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
	// weak_model resolution: string ref and None->self.
	pro := cfg.Models["pro"]
	if pro.WeakModel != flash {
		t.Error("pro.WeakModel should be flash")
	}
	if flash.WeakModel != flash {
		t.Error("flash.WeakModel should be itself (None => self)")
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
    "extra": model(p, "corp-extra", weak_model = "pro"),
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
	// Cross-file weak_model: project model references the user's alias
	// (resolution is post-merge).
	if cfg.Models["extra"].WeakModel != cfg.Models["pro"] {
		t.Error("extra.WeakModel should resolve to the user's pro model")
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
		{"reserved adapter", `
p = provider("anthropic", api_key = "k")
models = {"m": model(p, "s")}
default = "m"
`, "reserved"},
		{"missing weak alias", `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s", weak_model = "ghost")}
default = "m"
`, "ghost"},
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

func TestWeakModelInlineValue(t *testing.T) {
	src := `
p = provider("openai", api_key = "k")
cheap = model(p, "cheap-slug")
models = {"main": model(p, "main-slug", weak_model = cheap)}
default = "main"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	main := cfg.Models["main"]
	if main.WeakModel == nil || main.WeakModel.Slug != "cheap-slug" {
		t.Fatalf("weak = %+v", main.WeakModel)
	}
	// The inline weak model is its own weak model.
	if main.WeakModel.WeakModel != main.WeakModel {
		t.Error("inline weak model should self-resolve")
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

// TestVerifyParsesInDeclaredOrder pins the two properties the verify tool
// depends on: a dict of name -> argv, kept in the order it was written, because
// a bare verify run stops at the first failure and fast checks belong first.
func TestVerifyParsesInDeclaredOrder(t *testing.T) {
	src := userConfig + `
verify = {
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
	if got := cfg.VerifyNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("VerifyNames() = %v, want %v (declaration order is meaningful)", got, want)
	}
	if got := cfg.Verify[1].Argv; strings.Join(got, " ") != "go test ./..." {
		t.Errorf("verify[test] argv = %v", got)
	}
}

// TestVerifyProjectMergesPerKey covers the override the top-level placement is
// for: a project replaces one check and adds another without restating the
// user's set, and the user's ordering survives.
func TestVerifyProjectMergesPerKey(t *testing.T) {
	user := userConfig + `
verify = {
    "lint": ["user-lint"],
    "test": ["user-test"],
}
`
	project := `
verify = {
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

	want := []string{"lint", "test", "typecheck"}
	if got := cfg.VerifyNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("VerifyNames() = %v, want %v", got, want)
	}
	if got := strings.Join(cfg.Verify[0].Argv, " "); got != "user-lint" {
		t.Errorf("lint = %q, want the user's (untouched by the project)", got)
	}
	if got := strings.Join(cfg.Verify[1].Argv, " "); got != "project-test -race" {
		t.Errorf("test = %q, want the project's override in the user's slot", got)
	}
}

func TestVerifyRejectsBadShapes(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"not a dict", `verify = ["go", "test"]`},
		{"argv not a list", `verify = {"test": "go test"}`},
		{"argv element not a string", `verify = {"test": ["go", 1]}`},
		{"empty argv", `verify = {"test": []}`},
		{"empty name", `verify = {"  ": ["go"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(harness(t, userConfig+"\n"+tc.src+"\n", "", testEnv)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// TestVerifyAutoValidatesAgainstMergedChecks pins the property that makes the
// feature trustworthy: a name that isn't a real check fails at load. A typo
// here would otherwise mean the harness silently verifies nothing, which is the
// one failure mode automatic verification exists to prevent.
func TestVerifyAutoValidatesAgainstMergedChecks(t *testing.T) {
	good := userConfig + `
verify = {"lint": ["golangci-lint", "run"], "test": ["go", "test", "./..."]}
verify_auto = ["lint", "test"]
`
	cfg, err := Load(harness(t, good, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.VerifyAuto, ",") != "lint,test" {
		t.Errorf("VerifyAuto = %v", cfg.VerifyAuto)
	}

	bad := userConfig + `
verify = {"test": ["go", "test", "./..."]}
verify_auto = ["lnit"]
`
	_, err = Load(harness(t, bad, "", testEnv))
	if err == nil {
		t.Fatal("a verify_auto name with no matching check must fail at load")
	}
	if !strings.Contains(err.Error(), "lnit") || !strings.Contains(err.Error(), "test") {
		t.Errorf("error should name the typo and the real checks, got: %v", err)
	}
}

// TestVerifyAutoValidatesAfterTheProjectMerge covers why validation is
// post-merge: the project can supply the check the user's verify_auto names.
func TestVerifyAutoValidatesAfterTheProjectMerge(t *testing.T) {
	user := userConfig + "\nverify_auto = [\"typecheck\"]\n"
	project := `verify = {"typecheck": ["tsc", "--noEmit"]}`
	opts := harness(t, user, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(opts); err != nil {
		t.Errorf("a project-supplied check should satisfy the user's verify_auto: %v", err)
	}
}

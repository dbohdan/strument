package config

import (
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
	if !flash.RepoMap || flash.EditFormat != "diff" {
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

func TestEnvRequiredUnsetFailsLoad(t *testing.T) {
	_, err := Load(harness(t, userConfig, "", nil))
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvDefaultOnlyWhenNotRequired(t *testing.T) {
	src := `
p = provider("openai", api_key = env("MISSING", default = "fallback", required = False))
models = {"m": model(p, "gpt")}
default = "m"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models["m"].Provider.APIKey != "fallback" {
		t.Errorf("api key = %q", cfg.Models["m"].Provider.APIKey)
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
	opts.Warn = func(format string, args ...any) { warned = append(warned, format) }
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

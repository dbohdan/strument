package config

import (
	"strings"
	"testing"
)

const consultBase = `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "x"), "terra": model(p, "openai/gpt-6.1-terra")}
default = "m"
`

func loadConsult(t *testing.T, setting string) (*Config, error) {
	t.Helper()
	return Load(harness(t, consultBase+setting+"\n", "", testEnv))
}

func TestConsultModelsParsed(t *testing.T) {
	cfg, err := loadConsult(t, `consult_models = {"terra": "  documentation and user-facing prose "}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ConsultModels["terra"]; got != "documentation and user-facing prose" {
		t.Errorf("ConsultModels[terra] = %q", got)
	}
}

func TestConsultModelsUnsetIsEmpty(t *testing.T) {
	cfg, err := loadConsult(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ConsultModels) != 0 {
		t.Errorf("ConsultModels = %v, want none", cfg.ConsultModels)
	}
}

// Each refusal names what would have worked. The purpose is required because
// it is all the asking model has to decide by: an alias says which model, not
// when to reach for it.
func TestConsultModelsRefusals(t *testing.T) {
	for _, tc := range []struct {
		setting string
		want    []string
	}{
		{`consult_models = ["terra"]`, []string{"must be a dict"}},
		{`consult_models = {"terra": ""}`, []string{"terra", "say what to consult it for"}},
		{`consult_models = {"terra": 1}`, []string{"terra", "must be a string"}},
		{`consult_models = {"astra": "prose"}`, []string{"astra", "not a key of `models`", "terra"}},
	} {
		_, err := loadConsult(t, tc.setting)
		if err == nil {
			t.Errorf("%s: loaded, want an error", tc.setting)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q does not mention %q", tc.setting, err, w)
			}
		}
	}
}

// Validated after the merge: a trusted project may define the model a user's
// consult_models names.
func TestConsultModelsValidateAfterTheProjectMerge(t *testing.T) {
	user := userConfig + "\nconsult_models = {\"docs\": \"prose\"}\n"
	project := `models = {"docs": model(provider("openrouter", api_key = env("OPENROUTER_API_KEY")), "openai/gpt-6.1-terra")}`
	opts := harness(t, user, project, testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConsultModels["docs"] != "prose" {
		t.Errorf("ConsultModels = %v", cfg.ConsultModels)
	}
}

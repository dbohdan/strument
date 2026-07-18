package config

import "testing"

func TestModelReadableName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		slug    string
		display string
		want    string
	}{
		{"strip prefix and variant", "deepseek/deepseek-v4-pro:nitro", "", "deepseek-v4-pro"},
		{"strip prefix only", "openai/gpt-4o", "", "gpt-4o"},
		{"bare slug untouched", "gpt-4o", "", "gpt-4o"},
		{"last slash wins", "a/b/c", "", "c"},
		{"first colon wins", "x/y:z:w", "", "y"},
		{"display_name overrides", "deepseek/deepseek-v4-pro:nitro", "DeepSeek V4 Pro", "DeepSeek V4 Pro"},
		{"empty reduction falls back to slug", "vendor/:nitro", "", "vendor/:nitro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{Slug: tc.slug, DisplayName: tc.display}
			if got := m.ReadableName(); got != tc.want {
				t.Errorf("ReadableName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDisplayNameParsed(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"pro": model(p, "deepseek/deepseek-v4-pro:nitro", display_name = "DeepSeek V4 Pro")}
default = "pro"
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Models["pro"]
	if m.DisplayName != "DeepSeek V4 Pro" {
		t.Errorf("DisplayName = %q, want %q", m.DisplayName, "DeepSeek V4 Pro")
	}
	if got := m.ReadableName(); got != "DeepSeek V4 Pro" {
		t.Errorf("ReadableName() = %q, want the display_name", got)
	}
}

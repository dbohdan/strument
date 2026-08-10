package config

import (
	"strings"
	"testing"
)

const rdBase = `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "x")}
default = "m"
`

func loadRD(t *testing.T, setting string) (ReasoningDisplay, error) {
	t.Helper()
	src := rdBase
	if setting != "" {
		src += "reasoning_display = " + setting + "\n"
	}
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		return ReasoningDisplay{}, err
	}
	return cfg.ReasoningDisplay, nil
}

// The three forms, and the default. "full" is the zero value on purpose: a
// plain text stream cannot unfold what it hid, so showing less than everything
// is a thing to choose rather than to inherit.
func TestReasoningDisplayForms(t *testing.T) {
	for _, tc := range []struct {
		setting string
		want    ReasoningDisplay
	}{
		{"", ReasoningDisplay{Mode: ReasoningFull}}, // unset
		{`"full"`, ReasoningDisplay{Mode: ReasoningFull}},
		{`"off"`, ReasoningDisplay{Mode: ReasoningOff}},
		{"10", ReasoningDisplay{Mode: ReasoningCapped, Lines: 10}},
		{"1", ReasoningDisplay{Mode: ReasoningCapped, Lines: 1}},
		// Zero lines is "off" by the plainest reading of the number.
		{"0", ReasoningDisplay{Mode: ReasoningOff}},
	} {
		got, err := loadRD(t, tc.setting)
		if err != nil {
			t.Errorf("reasoning_display = %s: %v", tc.setting, err)
			continue
		}
		if got != tc.want {
			t.Errorf("reasoning_display = %s gave %+v, want %+v", tc.setting, got, tc.want)
		}
	}
}

func TestReasoningDisplayRejectsNonsense(t *testing.T) {
	for _, tc := range []struct{ setting, wants string }{
		{"-1", "negative"},
		{`"none"`, "reasoning_display"},
		{`["full"]`, "reasoning_display"},
		{"True", "reasoning_display"},
	} {
		_, err := loadRD(t, tc.setting)
		if err == nil {
			t.Errorf("reasoning_display = %s should not load", tc.setting)
			continue
		}
		if !strings.Contains(err.Error(), tc.wants) {
			t.Errorf("reasoning_display = %s: error %q should mention %q", tc.setting, err, tc.wants)
		}
	}
}

// A project's config replaces the user's whole-value, like every other
// top-level key.
func TestReasoningDisplayProjectOverrides(t *testing.T) {
	opts := harness(t, rdBase+`reasoning_display = "off"`+"\n", `reasoning_display = 5`+"\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := ReasoningDisplay{Mode: ReasoningCapped, Lines: 5}
	if cfg.ReasoningDisplay != want {
		t.Errorf("got %+v, want %+v", cfg.ReasoningDisplay, want)
	}
}

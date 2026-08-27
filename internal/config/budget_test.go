package config

import (
	"strings"
	"testing"
)

const budgetBase = `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "x")}
default = "m"
`

func loadBudget(t *testing.T, setting string) (*Config, error) {
	t.Helper()
	src := budgetBase
	if setting != "" {
		src += setting + "\n"
	}
	return Load(harness(t, src, "", testEnv))
}

// TestMaxStepsDefault: unset max_steps leaves Config.MaxSteps at 0 (the
// coder applies its own default of 25).
func TestMaxStepsDefault(t *testing.T) {
	cfg, err := loadBudget(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxSteps != 0 {
		t.Errorf("unset max_steps should be 0 (use coder default), got %d", cfg.MaxSteps)
	}
}

// TestMaxStepsParsed: a positive integer round-trips.
func TestMaxStepsParsed(t *testing.T) {
	cfg, err := loadBudget(t, "max_steps = 50")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxSteps != 50 {
		t.Errorf("max_steps = 50, got %d", cfg.MaxSteps)
	}
}

func TestMaxStepsRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ setting, wants string }{
		{"max_steps = 0", "at least 1"},
		{"max_steps = -1", "at least 1"},
		{`max_steps = "five"`, "positive integer"},
		{"max_steps = 1.5", "positive integer"},
	} {
		_, err := loadBudget(t, tc.setting)
		if err == nil {
			t.Errorf("max_steps = %s should not load", tc.setting)
			continue
		}
		if !strings.Contains(err.Error(), tc.wants) {
			t.Errorf("max_steps = %s: error %q should mention %q", tc.setting, err, tc.wants)
		}
	}
}

// TestMaxStepsProjectOverrides: a trusted project's setting replaces the
// user's, like every other whole-value top-level key.
func TestMaxStepsProjectOverrides(t *testing.T) {
	opts := harness(t, budgetBase+"max_steps = 10\n", "max_steps = 50\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxSteps != 50 {
		t.Errorf("max_steps = %d, want project override of 50", cfg.MaxSteps)
	}
}

// TestMaxErrorReflectionsDefault: unset leaves Config.MaxErrorReflections at 0
// (the coder applies its own default of 3).
func TestMaxErrorReflectionsDefault(t *testing.T) {
	cfg, err := loadBudget(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxErrorReflections != 0 {
		t.Errorf("unset max_error_reflections should be 0 (use coder default), got %d", cfg.MaxErrorReflections)
	}
}

// TestMaxErrorReflectionsParsed: a positive integer round-trips.
func TestMaxErrorReflectionsParsed(t *testing.T) {
	cfg, err := loadBudget(t, "max_error_reflections = 5")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxErrorReflections != 5 {
		t.Errorf("max_error_reflections = 5, got %d", cfg.MaxErrorReflections)
	}
}

func TestMaxErrorReflectionsRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ setting, wants string }{
		{"max_error_reflections = 0", "at least 1"},
		{"max_error_reflections = -1", "at least 1"},
		{`max_error_reflections = "three"`, "positive integer"},
		{"max_error_reflections = 2.5", "positive integer"},
	} {
		_, err := loadBudget(t, tc.setting)
		if err == nil {
			t.Errorf("max_error_reflections = %s should not load", tc.setting)
			continue
		}
		if !strings.Contains(err.Error(), tc.wants) {
			t.Errorf("max_error_reflections = %s: error %q should mention %q", tc.setting, err, tc.wants)
		}
	}
}

// TestMaxErrorReflectionsProjectOverrides: a trusted project's setting
// replaces the user's.
func TestMaxErrorReflectionsProjectOverrides(t *testing.T) {
	opts := harness(t, budgetBase+"max_error_reflections = 2\n", "max_error_reflections = 10\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxErrorReflections != 10 {
		t.Errorf("max_error_reflections = %d, want project override of 10", cfg.MaxErrorReflections)
	}
}

// TestDetectLoopsDefaultsOn: unset means on, which is the whole reason this one
// is a boolean rather than a budget — there is no "0 means use the default"
// value to hide behind.
func TestDetectLoopsDefaultsOn(t *testing.T) {
	cfg, err := loadBudget(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DetectLoops {
		t.Error("unset detect_loops should leave detection on")
	}
}

func TestDetectLoopsOff(t *testing.T) {
	cfg, err := loadBudget(t, "detect_loops = False")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DetectLoops {
		t.Error("detect_loops = False should turn detection off")
	}
}

func TestDetectLoopsRejectsNonBooleans(t *testing.T) {
	for _, setting := range []string{`detect_loops = "yes"`, "detect_loops = 1"} {
		_, err := loadBudget(t, setting)
		if err == nil {
			t.Errorf("%s should not load", setting)
			continue
		}
		if !strings.Contains(err.Error(), "must be a boolean") {
			t.Errorf("%s: error %q should say it must be a boolean", setting, err)
		}
	}
}

// A project can turn it off — a repo whose model output legitimately repeats
// (generated tables, fixtures) is exactly who needs to.
func TestDetectLoopsProjectOverrides(t *testing.T) {
	opts := harness(t, budgetBase, "detect_loops = False\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DetectLoops {
		t.Error("the project's detect_loops = False did not take effect")
	}
}

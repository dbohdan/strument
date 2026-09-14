package config

import (
	"strings"
	"testing"
)

func TestAutoApproveParsed(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "vendor/slug")}
default = "m"
auto_approve = ["websearch", "steps"]
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AutoApprove) != 2 || cfg.AutoApprove[0] != GrantWebsearch {
		t.Errorf("AutoApprove = %v", cfg.AutoApprove)
	}
}

// A misspelling must be a load error, not a permission that silently never
// applies -- which on screen is indistinguishable from the prompt being asked
// for a good reason.
func TestAutoApproveRejectsUnknownNames(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "vendor/slug")}
default = "m"
auto_approve = ["websearch", "webserch"]
`
	_, err := Load(harness(t, src, "", testEnv))
	if err == nil {
		t.Fatal("a misspelled prompt name loaded without complaint")
	}
	if !strings.Contains(err.Error(), "webserch") || !strings.Contains(err.Error(), "auto_approve") {
		t.Errorf("the error does not name the value or the key: %v", err)
	}
	// And it names what would have worked, the way --yes does.
	if !strings.Contains(err.Error(), GrantWebsearch) {
		t.Errorf("the error does not list the valid names: %v", err)
	}
}

// "all" is a word someone types, never a default -- but it has to keep working
// in config, since --yes takes it.
func TestAutoApproveAcceptsAll(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "vendor/slug")}
default = "m"
auto_approve = ["all"]
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	granted, err := ParseGrants("auto_approve", cfg.AutoApprove)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range GrantNames {
		if !granted[name] {
			t.Errorf("%q was not granted by \"all\"", name)
		}
	}
}

// The source name reaches the message, so the reader is told which of the two
// places to go and fix.
func TestParseGrantsNamesItsSource(t *testing.T) {
	_, err := ParseGrants("auto_approve", []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "auto_approve") {
		t.Errorf("err = %v", err)
	}
	_, err = ParseGrants("--yes", []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v", err)
	}
}

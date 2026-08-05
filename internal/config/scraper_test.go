package config

import (
	"slices"
	"strings"
	"testing"
)

const scraperBase = `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "x")}
default = "m"
`

// TestScraperParse: a `scraper` list round-trips to Config.Scraper argv.
func TestScraperParse(t *testing.T) {
	src := scraperBase + `scraper = ["chromium", "--headless=new", "--dump-dom", "%s"]` + "\n"
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"chromium", "--headless=new", "--dump-dom", "%s"}
	if !slices.Equal(cfg.Scraper, want) {
		t.Errorf("cfg.Scraper = %v, want %v", cfg.Scraper, want)
	}
}

// TestScraperUnset: no `scraper` leaves Config.Scraper empty (built-in HTTP path).
func TestScraperUnset(t *testing.T) {
	cfg, err := Load(harness(t, scraperBase, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Scraper) != 0 {
		t.Errorf("unset scraper should be empty, got %v", cfg.Scraper)
	}
}

func TestScraperRejectsNonList(t *testing.T) {
	src := scraperBase + `scraper = "chromium --dump-dom %s"` + "\n"
	if _, err := Load(harness(t, src, "", testEnv)); err == nil || !strings.Contains(err.Error(), "must be a list") {
		t.Fatalf("want list-type rejection, got %v", err)
	}
}

func TestScraperRejectsNonStringElement(t *testing.T) {
	src := scraperBase + `scraper = ["chromium", 7]` + "\n"
	if _, err := Load(harness(t, src, "", testEnv)); err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Fatalf("want non-string-element rejection, got %v", err)
	}
}

func TestScraperRejectsEmpty(t *testing.T) {
	src := scraperBase + `scraper = []` + "\n"
	if _, err := Load(harness(t, src, "", testEnv)); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("want empty rejection, got %v", err)
	}
}

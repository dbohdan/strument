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

// TestLoopDetectionDefaultsOn: unset leaves Config.NoLoopDetection false, which
// is what "use the built-in default" looks like for every other setting here.
// The field is negated for exactly this reason — see the note on it.
func TestLoopDetectionDefaultsOn(t *testing.T) {
	cfg, err := loadBudget(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoLoopDetection {
		t.Error("unset loop_detection should leave detection on")
	}
}

func TestLoopDetectionOff(t *testing.T) {
	cfg, err := loadBudget(t, "loop_detection = False")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NoLoopDetection {
		t.Error("loop_detection = False should turn detection off")
	}
}

// And True is not the same as unset saying nothing: it must survive a project
// config that would otherwise be free to differ.
func TestLoopDetectionOnIsExplicit(t *testing.T) {
	cfg, err := loadBudget(t, "loop_detection = True")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoLoopDetection {
		t.Error("loop_detection = True should leave detection on")
	}
}

func TestLoopDetectionRejectsNonBooleans(t *testing.T) {
	for _, setting := range []string{`loop_detection = "yes"`, "loop_detection = 1"} {
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
func TestLoopDetectionProjectOverrides(t *testing.T) {
	opts := harness(t, budgetBase, "loop_detection = False\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NoLoopDetection {
		t.Error("the project's loop_detection = False did not take effect")
	}
}

func TestWebfetchAllowParsed(t *testing.T) {
	cfg, err := loadBudget(t, `webfetch_allow = ["docs.python.org", "localhost:3000"]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebfetchAllow) != 2 || cfg.WebfetchAllow[0] != "docs.python.org" {
		t.Errorf("webfetch_allow = %q", cfg.WebfetchAllow)
	}
}

// A URL written where an origin belongs is a typo with a plausible shape. It is
// refused at load, because the alternative is an entry that silently never
// matches and a user who concludes the setting does not work.
func TestWebfetchAllowRejectsURLs(t *testing.T) {
	for _, setting := range []string{
		`webfetch_allow = ["https://docs.python.org"]`,
		`webfetch_allow = ["docs.python.org/3/"]`,
		`webfetch_allow = ["::1"]`,
		`webfetch_allow = [""]`,
		`webfetch_allow = [3000]`,
		`webfetch_allow = "docs.python.org"`,
	} {
		if _, err := loadBudget(t, setting); err == nil {
			t.Errorf("%s should not load", setting)
		}
	}
}

func TestWebfetchAllowProjectOverrides(t *testing.T) {
	opts := harness(t, budgetBase+`webfetch_allow = ["docs.python.org"]`+"\n",
		`webfetch_allow = ["localhost:3000"]`+"\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Whole-value, like env_allow: a project must be able to narrow what the
	// user's config widened, and merging could only ever widen.
	if len(cfg.WebfetchAllow) != 1 || cfg.WebfetchAllow[0] != "localhost:3000" {
		t.Errorf("webfetch_allow = %q, want the project's list alone", cfg.WebfetchAllow)
	}
}

// search() takes the backend as a string, like provider(), so a typo can be
// answered with what would have worked. That is the whole argument for the
// discriminator over one builtin per backend: Starlark's "undefined: searxngg"
// cannot name the alternative.
func TestSearchBackendTypoNamesTheAlternative(t *testing.T) {
	_, err := loadBudget(t, `websearch = search("searxngg", url="http://localhost:8888")`)
	if err == nil {
		t.Fatal("an unknown backend loaded")
	}
	if !strings.Contains(err.Error(), "searxng") {
		t.Errorf("the error did not name the backend that would have worked: %v", err)
	}
}

// A URL wrong in a way that looks right is refused at load, not at the first
// search — a config error that waits for the model to reach for a tool is a
// config error nobody sees.
func TestSearchRejectsBadURLsAtLoad(t *testing.T) {
	for _, src := range []string{
		`websearch = search("searxng")`,
		`websearch = search("searxng", url="")`,
		`websearch = search("searxng", url="localhost:8888")`,
		`websearch = search("searxng", url="ftp://localhost:8888")`,
		`websearch = search("searxng", url="http://localhost:8888/search?q=x")`,
		`websearch = search("searxng", url="http:///nohost")`,
		`websearch = "http://localhost:8888"`,
	} {
		if _, err := loadBudget(t, src); err == nil {
			t.Errorf("%s loaded without complaint", src)
		}
	}
}

func TestSearchParsedAndNormalized(t *testing.T) {
	cfg, err := loadBudget(t, `websearch = search("searxng", url="http://localhost:8888/", proxy="direct")`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebSearch == nil {
		t.Fatal("websearch was not parsed")
	}
	// The trailing slash goes, so joining "/search" cannot make "//search" —
	// which some reverse proxies in front of an instance will not route.
	if cfg.WebSearch.URL != "http://localhost:8888" {
		t.Errorf("URL = %q", cfg.WebSearch.URL)
	}
	// "direct" resolves to no proxy at load, the same as a provider's. This is
	// the case that matters: an instance on localhost has no business going
	// through a proxy meant for external traffic.
	if cfg.WebSearch.Proxy != "" {
		t.Errorf("proxy = %q, want it resolved to none", cfg.WebSearch.Proxy)
	}
	if cfg.WebSearch.Backend != SearchSearxNG {
		t.Errorf("backend = %q", cfg.WebSearch.Backend)
	}
}

// Unset is how a session has no search at all, and the tool is then not
// offered — there is nothing to fall back to, unlike webfetch.
func TestNoSearchByDefault(t *testing.T) {
	cfg, err := loadBudget(t, ``)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebSearch != nil {
		t.Errorf("a search backend appeared from nowhere: %+v", cfg.WebSearch)
	}
}

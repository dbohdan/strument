package config

import (
	"maps"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The TZ behavior these tests pin is a property of a *process*: Go caches the
// local zone on first use and never re-reads it. Both cases therefore run in
// fresh subprocesses of the test binary — asserting them in-process would let
// the first one decide the second, and would leak a frozen zone into every
// other test in the package.
const envSetHelperVar = "STRUMENT_ENVSET_HELPER"

func TestEnvSetHelper(t *testing.T) {
	mode := os.Getenv(envSetHelperVar)
	if mode == "" {
		t.Skip("helper process; driven by TestTimeZoneFromEnvSet")
	}
	if mode == "frozen" {
		// Render a time first, which is what latches the zone. This is the
		// future regression being simulated: a log line above config.Load.
		_ = time.Now().Format(time.RFC3339)
	}
	env := map[string]string{"TZ": "Asia/Tokyo"}
	if err := ApplyEnvSet(env); err != nil {
		t.Fatalf("ApplyEnvSet: %v", err)
	}
	// Printed rather than asserted here: the parent owns the expectations, so a
	// helper that silently did nothing cannot pass by not failing.
	//nolint:gosmopolitan // Reporting the process zone is what the helper is for.
	os.Stdout.WriteString("zone=" + time.Local.String() +
		" problem=" + map[bool]string{true: "yes", false: "no"}[TimeZoneProblem(env) != ""] + "\n")
}

func runEnvSetHelper(t *testing.T, mode string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestEnvSetHelper", "-test.v")
	cmd.Env = append(os.Environ(), envSetHelperVar+"="+mode, "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper (%s): %v\n%s", mode, err, out)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "zone=") {
			return line
		}
	}
	t.Fatalf("helper (%s) printed no result:\n%s", mode, out)
	return ""
}

// TestTimeZoneFromEnvSet checks both halves of the ordering hazard: that a TZ
// set early does reach Strument's own clock, and that one set late is reported
// rather than silently ignored. The second is the one that matters — the code
// is correct today, and the check exists so it says something the day it is
// not.
func TestTimeZoneFromEnvSet(t *testing.T) {
	if _, err := time.LoadLocation("Asia/Tokyo"); err != nil {
		t.Skip("no zoneinfo database on this machine")
	}

	if got, want := runEnvSetHelper(t, "early"), "zone=Asia/Tokyo problem=no"; got != want {
		t.Errorf("TZ set before any time is rendered: got %q, want %q", got, want)
	}
	// The helper runs with TZ=UTC, so a zone latched too early names itself
	// "UTC" rather than "Local" — which is why the predicate compares against
	// the zone that was asked for instead of looking for a sentinel. The first
	// version of this check looked for "Local" and passed nothing.
	if got, want := runEnvSetHelper(t, "frozen"), "zone=UTC problem=yes"; got != want {
		t.Errorf("TZ set after the zone is latched: got %q, want %q", got, want)
	}
}

// TestTimeZoneProblemIsQuietWhenItShouldBe guards the other direction. A
// warning that fires when nothing is wrong trains the user to ignore it, so no
// TZ in env_set means no claim either way.
func TestTimeZoneProblemIsQuietWhenItShouldBe(t *testing.T) {
	for _, env := range []map[string]string{nil, {}, {"GOFLAGS": "-mod=mod"}, {"TZ": ""}} {
		if msg := TimeZoneProblem(env); msg != "" {
			t.Errorf("TimeZoneProblem(%v) = %q, want silence: nothing asked for a zone", env, msg)
		}
	}
}

func TestApplyEnvSetReachesTheEnvironment(t *testing.T) {
	name := "STRUMENT_ENVSET_PROBE"
	t.Setenv(name, "before")
	if err := ApplyEnvSet(map[string]string{name: "after"}); err != nil {
		t.Fatalf("ApplyEnvSet: %v", err)
	}
	if got := os.Getenv(name); got != "after" {
		t.Errorf("%s = %q, want %q", name, got, "after")
	}
}

// TestEnvSetParsing covers the shapes a config can get wrong. The value-type
// message is the interesting one: it points at env() rather than just naming
// the type, because someone writing a non-string there is usually reaching for
// a value they should be sourcing from the environment anyway.
func TestEnvSetParsing(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   map[string]string // nil means the load must fail
		errHas string
	}{
		{
			name:   "a dict of names to values",
			source: `env_set = {"TZ": "Europe/Kyiv", "GOFLAGS": "-mod=mod"}`,
			want:   map[string]string{"TZ": "Europe/Kyiv", "GOFLAGS": "-mod=mod"},
		},
		{
			name:   "a value read from the environment",
			source: `env_set = {"GH_TOKEN": env("TEST_TOKEN", default = "fallback")}`,
			want:   map[string]string{"GH_TOKEN": "fallback"},
		},
		{name: "not a dict", source: `env_set = ["TZ"]`, errHas: "env_set"},
		{name: "a name with =", source: `env_set = {"TZ=x": "y"}`, errHas: "env_set"},
		{name: "an empty name", source: `env_set = {"": "y"}`, errHas: "env_set"},
		{name: "a non-string value", source: `env_set = {"TZ": 3}`, errHas: "env("},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(harness(t, userConfig+"\n"+tt.source+"\n", "", testEnv))
			if tt.want == nil {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("want an error mentioning %q, got %v", tt.errHas, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !maps.Equal(cfg.EnvSet, tt.want) {
				t.Errorf("EnvSet = %v, want %v", cfg.EnvSet, tt.want)
			}
		})
	}
}

// TestEnvSetProjectMergesPerEntry is the difference from env_allow, which a
// project replaces wholesale. env_allow is one decision about what the model
// may see and a project must be able to narrow it; env_set is a bag of
// independent settings, so a project naming TZ must not drop the user's
// GOFLAGS.
func TestEnvSetProjectMergesPerEntry(t *testing.T) {
	opts := harness(t,
		userConfig+"\nenv_set = {\"GOFLAGS\": \"-mod=mod\", \"TZ\": \"UTC\"}\n",
		"env_set = {\"TZ\": \"Europe/Kyiv\"}\n", testEnv)
	if _, err := TrustProject(opts.ProjectRoot, opts.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{"GOFLAGS": "-mod=mod", "TZ": "Europe/Kyiv"}
	if !maps.Equal(cfg.EnvSet, want) {
		t.Errorf("EnvSet = %v, want %v", cfg.EnvSet, want)
	}
}

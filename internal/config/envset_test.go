package config

import (
	"maps"
	"os"
	"strings"
	"testing"
	"time"
)

// TestApplyTimeZone covers the zone half of env_set. It runs in-process and on
// any platform, which the first version of these tests could not: they drove
// subprocesses to prove that an os.Setenv of TZ landed before the runtime
// latched the zone, and Windows then showed that no ordering saves it there —
// the runtime never reads TZ at all. Assigning time.Local removed the hazard
// the subprocesses existed to guard, so they went with it.
func TestApplyTimeZone(t *testing.T) {
	// time.Local is process-wide state; leaving it moved would put every later
	// test in the wrong zone.
	saved := time.Local                      //nolint:gosmopolitan // Saving the process zone to restore it.
	t.Cleanup(func() { time.Local = saved }) //nolint:gosmopolitan // Restoring it.

	tests := []struct {
		name     string
		env      map[string]string
		wantZone string // "" means time.Local must not move
		wantMsg  string // substring; "" means no message
	}{
		{name: "no TZ", env: map[string]string{"GOFLAGS": "-mod=mod"}},
		{name: "empty TZ", env: map[string]string{"TZ": ""}},
		{name: "nil map", env: nil},
		{name: "a database name", env: map[string]string{"TZ": "Asia/Tokyo"}, wantZone: "Asia/Tokyo"},
		{
			// A leading colon is POSIX-legal and not part of the name.
			name: "a colon-prefixed name", env: map[string]string{"TZ": ":Europe/Kyiv"},
			wantZone: "Europe/Kyiv",
		},
		{name: "UTC", env: map[string]string{"TZ": "UTC"}, wantZone: "UTC"},
		{
			name: "a typo", env: map[string]string{"TZ": "Europe/Kyev"},
			wantMsg: "is not a zone name",
		},
		{
			// Go does not implement POSIX rule strings. Under the old mechanism
			// this silently became UTC; now it is named.
			name: "a POSIX string with DST rules", env: map[string]string{"TZ": "EST5EDT4,M3.2.0/2"},
			wantMsg: "is not a zone name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			time.Local = saved //nolint:gosmopolitan // Resetting between subtests.
			msg := ApplyTimeZone(tt.env)

			switch {
			case tt.wantMsg == "" && msg != "":
				t.Errorf("ApplyTimeZone(%v) = %q, want silence", tt.env, msg)
			case tt.wantMsg != "" && !strings.Contains(msg, tt.wantMsg):
				t.Errorf("ApplyTimeZone(%v) = %q, want it to mention %q", tt.env, msg, tt.wantMsg)
			}

			//nolint:gosmopolitan // The process zone is what this asserts about.
			got := time.Local
			if tt.wantZone == "" {
				if got != saved {
					t.Errorf("time.Local moved to %q; nothing valid asked it to", got.String())
				}
				return
			}
			if got.String() != tt.wantZone {
				t.Errorf("time.Local = %q, want %q", got.String(), tt.wantZone)
			}
			// The point of moving it: what renders local now renders there.
			if zone := time.Now().Format("MST"); zone == "" {
				t.Errorf("time.Now() renders no zone after the move")
			}
			if loc := time.Now().Location().String(); loc != tt.wantZone {
				t.Errorf("time.Now().Location() = %q, want %q", loc, tt.wantZone)
			}
		})
	}
}

// TestApplyTimeZoneWorksAfterTheZoneIsLatched is the property that made
// assigning time.Local the answer rather than a bigger hammer. The runtime
// caches the local zone on first render and never re-reads TZ; on Windows it
// never reads TZ at all. Neither matters if the zone is assigned rather than
// signalled.
func TestApplyTimeZoneWorksAfterTheZoneIsLatched(t *testing.T) {
	saved := time.Local                      //nolint:gosmopolitan // Saving the process zone to restore it.
	t.Cleanup(func() { time.Local = saved }) //nolint:gosmopolitan // Restoring it.

	// Latch it, the way any earlier formatted timestamp would.
	_ = time.Now().Format(time.RFC3339)

	if msg := ApplyTimeZone(map[string]string{"TZ": "Asia/Tokyo"}); msg != "" {
		t.Fatalf("ApplyTimeZone: %s", msg)
	}
	if got := time.Now().Location().String(); got != "Asia/Tokyo" {
		t.Errorf("after a latch, time.Now().Location() = %q, want %q", got, "Asia/Tokyo")
	}
	// Persisted timestamps are written with an explicit .UTC() and must not
	// follow the session's zone into the files on disk.
	if got := time.Now().UTC().Location().String(); got != "UTC" {
		t.Errorf("explicit UTC became %q", got)
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

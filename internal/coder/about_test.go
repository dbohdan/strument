package coder

import (
	"strings"
	"testing"
	"time"
)

// The question the tool exists to settle is whether the process runs the
// build on disk. The answer is two timestamps, and the report states it rather
// than leaving the comparison to the model — in the direction it can go wrong.
func TestAboutSaysWhenTheBinaryIsNewerThanTheProcess(t *testing.T) {
	started := time.Date(2026, 9, 24, 14, 55, 45, 0, time.UTC)
	c := &Coder{Build: BuildInfo{
		Version:        "1.2.3",
		Commit:         "343abb0",
		GoVersion:      "go1.26.0",
		Executable:     "/usr/local/bin/strument",
		Started:        started,
		ExecutableTime: started.Add(-time.Minute),
	}}
	now := started.Add(time.Hour)
	const stale = "running an earlier build"

	if got := c.About(now); strings.Contains(got, stale) {
		t.Errorf("a binary built before the process started was called newer:\n%s", got)
	}
	c.Build.ExecutableTime = started.Add(time.Minute)
	if got := c.About(now); !strings.Contains(got, stale) {
		t.Errorf("a binary replaced after the process started went unmentioned:\n%s", got)
	}
}

func TestAboutReportsTheLiveClockAndTheBuild(t *testing.T) {
	c := &Coder{
		editFormat: "ask",
		Build:      BuildInfo{Version: "1.2.3", Commit: "343abb0", Modified: true, GoVersion: "go1.26.0"},
	}
	now := time.Date(2026, 9, 25, 0, 30, 0, 0, time.UTC)
	got := c.About(now)
	for _, want := range []string{
		"Version: 1.2.3",
		"Commit: 343abb0 (built with uncommitted changes)",
		"Go: go1.26.0",
		"Now (UTC): 2026-09-25 00:30:00 UTC, Friday",
		"Mode: ask",
		"Sandbox: none",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}

	// A build without a VCS stamp says so rather than printing nothing, and a
	// Coder with no session (strument tool about) leaves the mode out.
	c = &Coder{Build: BuildInfo{Version: "0.0.0-dev"}}
	got = c.About(now)
	if !strings.Contains(got, "Commit: unknown") {
		t.Errorf("an unstamped build did not say its commit is unknown:\n%s", got)
	}
	if strings.Contains(got, "Mode:") {
		t.Errorf("a Coder with no mode reported one:\n%s", got)
	}
}

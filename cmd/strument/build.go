package main

import (
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"dbohdan.com/strument/internal/coder"
)

// processStart is when this process started, near enough: package variables
// are initialized before main runs.
var processStart = time.Now()

// devVersion is what a build reports when it knows neither a stamped version
// nor its commit: a source tarball, or -buildvcs=false.
const devVersion = "dev"

// resolvedVersion is the version to report: the one the release build stamps
// with -X main.version, else a calendar version made from the commit the
// binary was built from, else devVersion. See pickVersion.
func resolvedVersion() string {
	var src versionSource
	if bi, ok := debug.ReadBuildInfo(); ok {
		src.module = bi.Main.Version
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				src.revision = s.Value
			case "vcs.time":
				src.time, _ = time.Parse(time.RFC3339, s.Value)
			case "vcs.modified":
				src.modified = s.Value == "true"
			}
		}
	}
	return pickVersion(version, src)
}

// versionSource is what the binary records about where it came from.
type versionSource struct {
	module   string    // bi.Main.Version: "v0.0.0-20261022102429-bf061db41dcb+dirty"
	revision string    // vcs.revision, full hash
	time     time.Time // vcs.time, the commit's
	modified bool      // vcs.modified
}

// pseudoVersion matches the tail of a Go module pseudo-version in all three of
// its forms (vX.0.0-T-H, vX.Y.Z-pre.0.T-H, vX.Y.(Z+1)-0.T-H): a UTC commit
// time and a 12-digit hash, with +dirty for a build from a modified checkout.
var pseudoVersion = regexp.MustCompile(`(\d{14})-([0-9a-f]{12})(\+dirty)?$`)

// pickVersion chooses the version to report. Strument uses calendar versions,
// 2026.10.31-abc1def: the commit's date in UTC, then its short hash, with
// +dirty when the working tree had changes. The date is the commit's, not the
// build's, so one commit always reports one version; UTC because that is what
// Go's pseudo-versions use, and a checkout's build and a `go install` of the
// same commit should agree.
//
// The sources, in order: a version stamped with -X main.version; the vcs.*
// settings a build from a checkout records; the module pseudo-version, which is
// all a `go install …@latest` has, the proxy's zip carrying no .git; a module
// version that is not a pseudo-version, from a tag, reported as it is; and
// devVersion. Whatever it is, it is bare: the "v" Go puts on module versions
// is dropped.
func pickVersion(stamped string, src versionSource) string {
	if stamped != devVersion {
		return strings.TrimPrefix(stamped, "v")
	}
	if src.revision != "" && !src.time.IsZero() {
		return calendarVersion(src.time, src.revision, src.modified)
	}
	if m := pseudoVersion.FindStringSubmatch(src.module); m != nil {
		t, err := time.Parse("20060102150405", m[1])
		if err == nil {
			return calendarVersion(t, m[2], m[3] != "")
		}
	}
	if src.module != "" && src.module != "(devel)" {
		return strings.TrimPrefix(src.module, "v")
	}
	return devVersion
}

// calendarVersion formats a commit as 2026.10.31-abc1def[+dirty]. The month
// and day are zero-padded, so versions sort as plain strings.
func calendarVersion(t time.Time, hash string, dirty bool) string {
	v := t.UTC().Format("2006.01.02") + "-" + hash[:min(7, len(hash))]
	if dirty {
		v += "+dirty"
	}
	return v
}

// buildInfo is what this binary knows about how it was built, for the about
// tool.
func buildInfo() coder.BuildInfo {
	b := coder.BuildInfo{
		Version:   resolvedVersion(),
		GoVersion: runtime.Version(),
		Started:   processStart,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		b.GoVersion = bi.GoVersion
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Commit = s.Value
			case "vcs.time":
				b.CommitTime, _ = time.Parse(time.RFC3339, s.Value)
			case "vcs.modified":
				b.Modified = s.Value == "true"
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		b.Executable = exe
		if fi, err := os.Stat(exe); err == nil {
			b.ExecutableTime = fi.ModTime()
		}
	}
	return b
}

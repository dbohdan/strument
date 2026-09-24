package main

import (
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"dbohdan.com/strument/internal/coder"
)

// processStart is when this process started, near enough: package variables
// are initialized before main runs.
var processStart = time.Now()

// devVersion is what an unstamped build reports.
const devVersion = "0.0.0-dev"

// resolvedVersion is the version to report: the one the release build stamps
// with -X main.version, else the module version `go install …@v1.2.3` records
// in the binary, else devVersion. The middle case is why this is not just the
// variable: an installed build has a real version and never passes through
// the release script.
func resolvedVersion() string {
	if version != devVersion {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
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

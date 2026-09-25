package main

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
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
	module := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		module = bi.Main.Version
	}
	return pickVersion(version, module)
}

// pickVersion chooses between the stamped version and the module's, and
// returns it bare: "1.2.3", never "v1.2.3". Whoever displays it adds the "v"
// — the banner, the release file name — so a source that carries its own
// printed "Strument vv0.0.0-…". Go's module versions always do (a checkout's
// `go build` records a pseudo-version like "v0.0.0-20260924235244-10c205370749"),
// and a VERSION given as "v1.2.3" would.
func pickVersion(stamped, module string) string {
	v := stamped
	if stamped == devVersion && module != "" && module != "(devel)" {
		v = module
	}
	return strings.TrimPrefix(v, "v")
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

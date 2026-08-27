package config

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	// The zone database, compiled in. LoadLocation otherwise reads the host's
	// copy, which Windows does not have one of at all and a scratch container
	// may not either — so `env_set = {"TZ": "Europe/Kyiv"}` would work on the
	// developer's laptop and fail on exactly the machines this setting exists
	// for. About 410 KB of binary, paid once, for a setting that either works
	// everywhere or is not worth having.
	_ "time/tzdata"
)

// ApplyEnvSet puts the config's `env_set` into Strument's own environment.
//
// One os.Setenv reaches every consumer, because of how each already gets its
// environment: git inherits it (gitrepo runs exec.Command with a nil Env),
// /run inherits it (PipeRunner's zero value is "the whole environment"), and a
// model-run command sees it only if the name is on the allowlist, since
// FilterEnv builds from os.Environ. So this changes what commands see without
// changing what the model may see — a value written in a config file still has
// to be allowed through separately.
//
// Sorted, so a failure reports the same name every run rather than whichever
// one the map happened to yield first.
func ApplyEnvSet(env map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(env)) {
		if err := os.Setenv(name, env[name]); err != nil {
			return fmt.Errorf("env_set: %s: %w", name, err)
		}
	}
	return nil
}

// ApplyTimeZone makes a TZ in env_set govern Strument's own clock, and returns
// a message to show the user when it cannot. Setting the variable is not
// enough, for two different reasons on two different platforms.
//
// On Unix the runtime reads TZ once, lazily, the first time anything renders a
// local time, and caches it — so an os.Setenv only lands if it happens before
// the first formatted timestamp, an ordering no test can see and one future log
// line would undo. On Windows the runtime never reads TZ at all: initLocal
// calls GetTimeZoneInformation and names the result "Local" regardless, so the
// same setenv reached commands and git and left Strument's own dates in the
// machine's zone. CI found the second; the first was waiting.
//
// Assigning time.Local answers both. It is a package-level variable, and
// measured: the assignment takes effect even after the zone has been latched,
// on any platform, and every local rendering follows it — the date in the
// prompt, the timestamps on transcript turns. Explicit .UTC() calls are
// untouched, which is what keeps the persisted files (resume, undo, cost) in
// UTC where they belong.
//
// Called once at startup, before there is a second goroutine to race with.
//
// The only remaining failure is a zone name the database does not have, which
// includes a POSIX TZ string carrying its own DST rules ("EST5EDT4,M3.2.0/2").
// Under the old mechanism those silently became UTC. Now they are named.
func ApplyTimeZone(env map[string]string) string {
	// A leading colon is POSIX-legal and not part of the zone's name.
	tz := strings.TrimPrefix(env["TZ"], ":")
	if tz == "" {
		return ""
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Sprintf(
			"strument: env_set TZ %q is not a zone name (%v). Commands and git still get TZ, but "+
				"Strument's own dates stay in this machine's zone. Use a database name like "+
				"\"Europe/Kyiv\" or \"UTC\".", tz, err)
	}
	// gosmopolitan flags time.Local because depending on the local zone is
	// usually a bug. Setting it is the opposite: it is how the user's stated zone
	// becomes the one this process renders in.
	time.Local = loc //nolint:gosmopolitan // Assigning the process zone is the point.
	return ""
}

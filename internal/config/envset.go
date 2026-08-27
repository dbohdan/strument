package config

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
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

// TimeZoneProblem reports that a TZ in env_set did not reach Strument's own
// clock, and returns "" when it did or when none was asked for.
//
// TZ needs a check the other variables do not, because the runtime reads it
// once and caches the answer. Measured: time.Now() and time.Since do not
// trigger that, Format does — so setting TZ works only ahead of the first
// rendered timestamp, which is true today and which one future log line above
// config.Load would silently undo. Rather than leave that to a test that cannot
// see the ordering it depends on, the program checks itself.
//
// time.Local.String() is the predicate, and it is exact rather than
// conservative because it was measured to be exact. It comes back equal to TZ
// for every form the runtime actually honors, plain names and zoneinfo-backed
// POSIX ones alike, and differs whenever the zone did not take — whether
// because the name is unknown, because it is a POSIX string with DST rules
// (which Go does not implement and silently answers with UTC), or because the
// zone was latched before this ran. One symptom, several causes, so the message
// names the causes and reports the zone Strument actually ended up in, which is
// the part that says which one it was.
//
// Reading Local is itself what initializes it, so this has to be called after
// ApplyEnvSet. That is not a caveat; it is the mechanism.
func TimeZoneProblem(env map[string]string) string {
	// A leading colon is POSIX-legal and not part of the zone's name.
	tz := strings.TrimPrefix(env["TZ"], ":")
	if tz == "" {
		return ""
	}
	// gosmopolitan flags time.Local because assuming the local zone is usually
	// a bug. Here the local zone is the subject: the question is which one this
	// process ended up in, and nothing else can answer it.
	got := time.Local.String() //nolint:gosmopolitan // Reading the process zone is the check.
	if got == tz {
		return ""
	}
	return fmt.Sprintf(
		"strument: env_set TZ %q did not take; Strument's own clock is %q. Commands and git still get "+
			"TZ — only Strument's timestamps, the date in the prompt and the commits it makes, are in the "+
			"other zone. Either the name is not a zone this machine knows, or something rendered a time "+
			"before the config was read.", tz, got)
}

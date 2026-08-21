package repl

import (
	"context"
	"os"
	"strings"
)

func displayPath(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return path
	}
	return strings.TrimRight(path, "/") + "/"
}

// cmdSandbox answers "what can this session write?".
//
// The writable set is the whole security decision, and it is derived rather
// than declared — from the project, the state directory, TMPDIR, whichever
// toolchain caches this machine has, and `sandbox_write`. Nobody should have to
// read that derivation and guess the result on their own machine, least of all
// before deciding how freely to approve commands.
//
// It is the sibling of /env, which answers the same shape of question about the
// environment model-run commands see.
func cmdSandbox(_ context.Context, r *REPL, _ string) string {
	sb := r.coder.Sandbox

	switch {
	case sb.Active:
		r.printf("Sandbox: on (Landlock).")
	case sb.Required:
		r.out.Errorf("Sandbox: required but unavailable (%s).", sb.Unavailable)
		r.printf("Nothing the model can cause to run will run. /run still works — you typed it.")
		r.printf(`Set sandbox = "" in your config to work without one.`)
		return ""
	default:
		r.printf(`Sandbox: off. Set sandbox = "landlock" to confine writes.`)
		return ""
	}

	r.printf("Reads and running programs are not restricted. Writes are permitted only under:")
	for _, p := range sb.Writable {
		r.printf("  %s", displayPath(p))
	}
	// Said plainly, because it is the half people get wrong about this feature:
	// it buys integrity, not confidentiality.
	r.printf("Anything else on the filesystem can be read but not written.")
	// A sandbox_write entry that was not there to grant. Said plainly because
	// the alternative is finding out from a denied command that the setting
	// looked applied and was not.
	for _, p := range sb.Skipped {
		r.out.Errorf("Not granted: %s does not exist, so nothing was granted for it.", p)
	}
	r.printf("Add a path with sandbox_write in your config; it cannot be changed mid-session.")
	return ""
}

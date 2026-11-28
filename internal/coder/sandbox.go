package coder

import (
	"path/filepath"
	"regexp"
	"strings"
)

// SandboxState is what the harness knows about its own confinement. The coder
// does not apply the sandbox — that happens once, at startup, before any model
// interaction — but it has to know, because two things depend on it: whether a
// model-caused command may run at all, and what to tell the model when one
// fails.
type SandboxState struct {
	// Required is true when the config asked for a sandbox.
	Required bool
	// Active is true when one is actually enforcing.
	Active bool
	// Writable lists the roots writes may land under, for the message shown
	// when something is denied. It is the *enforced* set, not the derived
	// one: a path that did not exist when the ruleset went in grants nothing,
	// and reporting it would promise a write that is about to be refused.
	Writable []string
	// Skipped are paths the user asked for in sandbox_write that were not
	// there to grant. Only the user's own entries, never the derived defaults
	// — most machines have no ~/.m2, and saying so every session is noise,
	// whereas a sandbox_write path silently doing nothing is the surprise
	// worth naming.
	Skipped []string
	// Unavailable is why there is no sandbox, when one was required.
	Unavailable string
}

// blocksExecution reports whether a model-caused command must be refused.
//
// The distinction this encodes is the one that makes a required sandbox
// tolerable. A kernel without Landlock does not stop Strument from starting,
// and does not stop the user reading, editing, or committing; it stops the
// *model* from running commands. That is fail-closed — nothing runs
// unsandboxed — without bricking the tool over a kernel option.
//
// It is deliberately not a warning. A mode that says "no sandbox today" and
// then runs the command anyway trains the user to ignore the line, and the one
// session where it mattered looks exactly like the fifty where it did not.
func (s SandboxState) blocksExecution() bool { return s.Required && !s.Active }

// refusal is what the model is told instead of a result.
//
// It names the setting, because the model cannot fix a kernel but the user can
// change a config, and a result that says only "refused" invites the model to
// try the same command three more ways.
func (s SandboxState) refusal() string {
	why := s.Unavailable
	if why == "" {
		why = "no sandbox is active"
	}
	return "Refused: this session requires a sandbox and " + why + ". " +
		"Commands the model causes to run are disabled. " +
		"The user can run the command themselves with /run, or set `sandbox = \"\"` in their config to work without one."
}

// deniedHint is appended to the output of a failed command when the sandbox is
// active and the failure looks like the sandbox caused it.
//
// It exists because of how a denial actually arrives. A model that gets an
// unexplained failure edits the code, and a permission error it cannot place
// is exactly the kind of thing it will try to "fix" — three times, in three
// ways, none of which is the problem.
func (s SandboxState) deniedHint() string {
	if len(s.Writable) == 0 {
		return ""
	}
	return "\nStrument's sandbox may have denied this: writes are permitted only under " +
		strings.Join(s.Writable, ", ") +
		". Reading and running programs are not restricted. This is not something to work around by editing code — " +
		"tell the user, who can add a path to `sandbox_write`."
}

// looksRefused matches EACCES and EPERM, the way a denied write reads.
func looksRefused(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{"permission denied", "operation not permitted", "eacces"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// looksCrossDevice matches EXDEV, the way a denied *rename between
// directories* reads: "invalid cross-device link". A live probe confirmed
// os.IsPermission is false for it. It is both the likeliest breakage under
// this policy and the one that looks least like a sandbox: a program seeing
// EXDEV concludes it crossed a filesystem boundary, and mv(1) quietly falls
// back to copying instead of reporting anything at all.
func looksCrossDevice(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "cross-device link") || strings.Contains(lower, "exdev")
}

// mayHaveDenied reports whether a failed command's output could be the
// sandbox refusing something.
//
// The sandbox refuses writes outside the writable roots and nothing else, so a
// denial on a path it permits, or on one the ordinary file permissions already
// refuse, is not its doing. The case that showed it: `cat /proc/<pid>/environ`
// of a non-dumpable process, a read denied by the kernel's own rules, came back
// telling the model that writes are confined, which is true and beside the
// point. A line naming no absolute path still gets the hint, because nothing
// can be ruled out, and so does EXDEV: Landlock refuses a rename between two
// writable directories with it too.
func (s SandboxState) mayHaveDenied(output string) bool {
	for line := range strings.Lines(output) {
		if looksCrossDevice(line) {
			return true
		}
		if !looksRefused(line) {
			continue
		}
		paths := absPaths(line)
		if len(paths) == 0 {
			return true
		}
		for _, p := range paths {
			if !s.permits(p) && !dacRefusesWrite(p) {
				return true
			}
		}
	}
	return false
}

// permits reports whether path is under a writable root. Both are cleaned
// and compared with the platform's separator: Clean turns a Windows path's
// slashes into backslashes, and a root compared as written then contains
// nothing.
func (s SandboxState) permits(path string) bool {
	sep := string(filepath.Separator)
	path = filepath.Clean(path)
	for _, root := range s.Writable {
		root = filepath.Clean(root)
		if path == root || strings.HasPrefix(path, strings.TrimSuffix(root, sep)+sep) {
			return true
		}
	}
	return false
}

// pathInLine finds absolute paths in an error line: after the start, a space,
// a quote of any kind, or the "=" and "(" of strace-like output, up to the next
// space, quote, colon or comma.
var pathInLine = regexp.MustCompile("(?:^|[\\s'\"`‘’“”=(])(/[^\\s'\"`‘’“”:,;)]+)")

func absPaths(line string) []string {
	ms := pathInLine.FindAllStringSubmatch(line, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.TrimRight(m[1], "."))
	}
	return out
}

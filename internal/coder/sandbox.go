package coder

import "strings"

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
	// when something is denied.
	Writable []string
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

// looksDenied reports whether command output suggests the sandbox refused
// something.
//
// Two patterns, not one, and the second is the reason this is a function
// rather than a substring check. A denied *write* is EACCES and reads as
// "permission denied". A denied *rename between directories* is EXDEV and
// reads as "invalid cross-device link" — a live probe confirmed os.IsPermission
// is false for it. That second one is both the likeliest breakage under this
// policy and the one that looks least like a sandbox: a program seeing EXDEV
// concludes it crossed a filesystem boundary, and mv(1) quietly falls back to
// copying instead of reporting anything at all.
func looksDenied(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"permission denied",
		"operation not permitted",
		"invalid cross-device link",
		"cross-device link",
		"eacces",
		"exdev",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

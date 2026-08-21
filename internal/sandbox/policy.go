package sandbox

import "os"

// Policy is what the sandbox permits. Reads and execution are unrestricted;
// this is only ever a list of places writes may land.
type Policy struct {
	// Writable are the roots writes are permitted under, absolute and
	// symlink-resolved by the caller. A path that does not exist is skipped
	// rather than failing the policy: a project may have no state directory
	// yet, and a toolchain cache appears the first time the toolchain runs.
	Writable []string
}

// Granted is the subset of Writable a rule can actually be anchored to.
//
// Landlock resolves a path to an inode when the ruleset is installed, so a path
// that does not exist grants nothing: writeRule skips it, and the enforced
// policy is quietly narrower than the list it was built from. Skipping is the
// right behavior — a project need not have a state directory yet, and most
// machines have no ~/.m2 — but it is the wrong thing to *report*. A live run
// caught /sandbox listing a cache directory immediately above a build that was
// then refused permission to create it.
//
// Membership is decided when the policy is applied and does not change
// afterwards. A directory created later in the session is still not writable,
// because there was nothing to anchor a rule to when the ruleset went in.
func (p Policy) Granted() []string {
	out := make([]string, 0, len(p.Writable))
	for _, path := range p.Writable {
		if _, err := os.Stat(path); err == nil {
			out = append(out, path)
		}
	}
	return out
}

// writableDevices are the device files a build needs to *write*, as opposed to
// read. Reads are already covered by the read rule over "/", so this list is
// short and every entry earned its place:
//
//   - /dev/null is opened for writing by approximately every shell command
//     ever written. A live probe confirmed a read-only "/" denies it.
//   - /dev/ptmx and /dev/pts are how a pty is allocated, and /dev/ptmx is
//     opened O_RDWR. Without them anything that wants a terminal fails,
//     including Strument's own opt-in PTY execution.
//   - /dev/tty is opened O_RDWR by programs that want the terminal directly
//     rather than through the file descriptors they were handed.
//   - /dev/zero and /dev/full are opened O_RDWR by allocators and by test
//     suites checking write-error handling.
//   - /dev/shm is POSIX shared memory: Python's multiprocessing, Chromium,
//     and a good deal of test tooling put real files there.
//
// The whole of /dev is deliberately *not* writable. Under a policy whose entire
// claim is integrity, /dev/sda is exactly the wrong thing to hand over.
var writableDevices = []string{
	"/dev/null",
	"/dev/zero",
	"/dev/full",
	"/dev/tty",
	"/dev/ptmx",
	"/dev/pts",
	"/dev/shm",
}

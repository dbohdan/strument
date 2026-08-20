package sandbox

// Policy is what the sandbox permits. Reads and execution are unrestricted;
// this is only ever a list of places writes may land.
type Policy struct {
	// Writable are the roots writes are permitted under, absolute and
	// symlink-resolved by the caller. A path that does not exist is skipped
	// rather than failing the policy: a project may have no state directory
	// yet, and a toolchain cache appears the first time the toolchain runs.
	Writable []string
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

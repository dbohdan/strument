package main

import "golang.org/x/sys/unix"

// undumpable marks this process non-dumpable, which makes its /proc entries
// (environ, mem, maps) readable only by root and stops a same-user process
// from attaching with ptrace.
//
// The reason is the bash tool. The environment allowlist keeps API keys out of
// the commands the model runs, but those commands run as the same user as
// Strument, and Strument's own environment still holds the keys it was started
// with: in a contained trial, `cat /proc/$PPID/environ` from the bash tool
// printed them. Non-dumpable closes that for Strument's own process. It cannot
// do it for the shell that started Strument, whose environment is its own.
//
// The flag does not pass to children: execve resets it, so the commands the
// model runs are ordinary processes. Strument itself no longer writes a core
// dump, and a debugger has to start it rather than attach to it.
func undumpable() error {
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}

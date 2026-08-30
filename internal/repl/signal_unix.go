//go:build unix

package repl

import "syscall"

// interruptSignal is the user-space interrupt: SIGUSR1 on every Unix, chosen
// over SIGUSR2 only so a program composing signals has the odd one left free.
// It is the keyboard-free way to stop a send — a remote assistant or a script
// cannot reach the terminal's ISIG, but `kill -USR1` reaches the process.
const interruptSignal = syscall.SIGUSR1

// UserInterruptSignal reports the signal the REPL and script mode treat as a
// stop-the-send interrupt, for a caller wiring its own signal.NotifyContext —
// main, which has no REPL to subscribe for it.
func UserInterruptSignal() syscall.Signal { return interruptSignal }

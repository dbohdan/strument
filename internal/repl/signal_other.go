//go:build !unix

package repl

import "syscall"

// interruptSignal on a platform with no user signals: Signal(0) is never
// delivered, so the subscription below is inert rather than a build error.

// UserInterruptSignal reports the signal the REPL and script mode treat as a
// stop-the-send interrupt. On a platform with no user signals it is
// Signal(0), which is never delivered, so subscribing to it is inert.
func UserInterruptSignal() syscall.Signal { return interruptSignal }

const interruptSignal = syscall.Signal(0)

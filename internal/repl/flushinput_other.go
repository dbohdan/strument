//go:build !linux && !darwin

package repl

import "io"

// flushTerminalInput is a no-op where the terminal's input queue cannot be
// flushed the POSIX way.
func flushTerminalInput(io.Reader) int { return 0 }

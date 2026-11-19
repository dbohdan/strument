//go:build linux || darwin

package repl

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// flushTerminalInput discards what was typed into the terminal and not yet
// read, and reports how many bytes that was. A reader that is not a
// terminal is left alone.
func flushTerminalInput(r io.Reader) int {
	f, ok := r.(*os.File)
	if !ok {
		return 0
	}
	fd := int(f.Fd())
	attrs, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return 0
	}
	n, _ := unix.IoctlGetInt(fd, ioctlInputPending)
	if err := unix.IoctlSetTermios(fd, ioctlFlushTermios, attrs); err != nil {
		return 0
	}
	return n
}

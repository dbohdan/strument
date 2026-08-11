//go:build darwin

package repl

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TIOCGETA
const ioctlWriteTermios = unix.TIOCSETA

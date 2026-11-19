//go:build darwin

package repl

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TIOCGETA
const ioctlWriteTermios = unix.TIOCSETA

// ioctlFlushTermios sets the attributes after discarding unread input.
const ioctlFlushTermios = unix.TIOCSETAF

// ioctlInputPending reads how many bytes are waiting to be read. FIONREAD is
// _IOR('f', 127, int) in <sys/filio.h>; x/sys/unix does not export it for
// darwin.
const ioctlInputPending = 0x4004667f

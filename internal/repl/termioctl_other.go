//go:build linux

package repl

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TCGETS
const ioctlWriteTermios = unix.TCSETS

// ioctlFlushTermios sets the attributes after discarding unread input.
const ioctlFlushTermios = unix.TCSETSF

// ioctlInputPending reads how many bytes are waiting to be read.
const ioctlInputPending = unix.TIOCINQ

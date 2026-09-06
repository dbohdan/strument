//go:build windows

package platform

import (
	"syscall"

	"dbohdan.com/strument/internal/readline/internal/term"
)

const (
	IsWindows = true
)

// SuspendProcess has nothing to do on Windows, which has no SIGTSTP. It
// reports false — nothing was suspended — for the same reason the Unix version
// reports anything at all: the caller says so rather than leaving a cleared
// prompt and no explanation. The Ctrl-Z key is not routed here at all (see
// operation.go's platform.IsWindows guard), so this is only for completeness.
func SuspendProcess() bool {
	return false
}

// GetScreenSize returns the width, height of the terminal or -1,-1
func GetScreenSize() (width int, height int) {
	width, height, err := term.GetSize(int(syscall.Stdout))
	if err == nil {
		return width, height
	} else {
		return 0, 0
	}
}

func DefaultIsTerminal() bool {
	return term.IsTerminal(int(syscall.Stdin)) && term.IsTerminal(int(syscall.Stdout))
}

func DefaultOnWidthChanged(f func()) {
	DefaultOnSizeChanged(f)
}

func DefaultOnSizeChanged(f func()) {
	// TODO: does Windows have a SIGWINCH analogue?
}

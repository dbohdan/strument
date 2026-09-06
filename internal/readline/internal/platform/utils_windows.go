//go:build windows

package platform

import (
	"syscall"

	"dbohdan.com/strument/internal/readline/internal/term"
)

const (
	IsWindows = true
)

// SuspendProcess has nothing to do on Windows, which has no SIGTSTP. The Ctrl-Z
// key is not routed here either (see operation.go's platform.IsWindows guard),
// so this exists only to keep the package building.
func SuspendProcess() {
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

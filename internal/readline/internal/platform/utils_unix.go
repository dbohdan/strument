//go:build aix || darwin || dragonfly || freebsd || (linux && !appengine) || netbsd || openbsd || os400 || solaris || zos

package platform

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"dbohdan.com/strument/internal/readline/internal/term"
)

const (
	IsWindows = false
)

// suspendGrace bounds the wait for the SIGCONT that ends a suspension.
//
// It exists to keep the ordering right in the ordinary case — the caller
// restores raw mode when this returns, and doing that before the stop actually
// lands would hand the shell a raw terminal — while making sure a suspend that
// never happens costs a pause rather than the session.
const suspendGrace = 250 * time.Millisecond

// SuspendProcess suspends the foreground process group with SIGTSTP and returns
// once the process is resumed, or after suspendGrace if it never stopped.
//
// It signals the process *group* (pid 0) rather than just this process, which is
// what the terminal driver does when it sees the suspend character. That is the
// change that fixed a reported double Ctrl-Z: with a self-directed signal, the
// first press cleared the line, put the terminal back into cooked mode and then
// stopped nothing, leaving the harness blocked here while the user's typing was
// echoed by the kernel — which looks exactly like a working prompt that has
// quietly stopped accepting input. Why the self-directed form failed on that
// machine was never established; the kernel-generated signal on the same
// terminal stopped the process, and it does not reproduce elsewhere.
//
// The wait is bounded so that failure costs a pause instead of the session.
//
// It deliberately does *not* try to report whether the suspend worked. Two
// attempts at that both shipped and both cried wolf on a working suspend. The
// first waited for a SIGCONT, which cannot arrive twice: the one that ends the
// stop is spent resuming us. The second timed the kill, on the theory that a
// self-group signal is delivered before it returns — a capture then showed the
// complaint printed *before* the shell reported the job stopped, because Go
// routes the signal through its own runtime and the stop lands afterwards.
// Whether we are about to be stopped is not a question this process can answer
// about itself from in here, and a false alarm on a suspend that worked is worse
// than saying nothing about one that did not.
func SuspendProcess() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGCONT)
	defer stop()

	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		return
	}

	timer := time.NewTimer(suspendGrace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// getWidthHeight of the terminal using given file descriptor
func getWidthHeight(stdoutFd int) (width int, height int) {
	width, height, err := term.GetSize(stdoutFd)
	if err != nil {
		return -1, -1
	}
	return
}

// GetScreenSize returns the width/height of the terminal or -1,-1 or error
func GetScreenSize() (width int, height int) {
	width, height = getWidthHeight(syscall.Stdout)
	if width < 0 {
		width, height = getWidthHeight(syscall.Stderr)
	}
	return
}

func DefaultIsTerminal() bool {
	return term.IsTerminal(syscall.Stdin) && (term.IsTerminal(syscall.Stdout) || term.IsTerminal(syscall.Stderr))
}

// -----------------------------------------------------------------------------

var (
	sizeChange         sync.Once
	sizeChangeCallback func()
)

func DefaultOnWidthChanged(f func()) {
	DefaultOnSizeChanged(f)
}

func DefaultOnSizeChanged(f func()) {
	sizeChangeCallback = f
	sizeChange.Do(func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)

		go func() {
			for {
				_, ok := <-ch
				if !ok {
					break
				}
				sizeChangeCallback()
			}
		}()
	})
}

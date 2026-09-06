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

// suspendGrace is how long SuspendProcess waits to find out whether the
// SIGTSTP it raised actually stopped the process. A self-directed signal is
// delivered before the kill returns, so a stop that is going to happen has
// happened; this only has to be long enough not to race the runtime's signal
// goroutine.
const suspendGrace = 250 * time.Millisecond

// SuspendProcess suspends the foreground process group with SIGTSTP and returns
// once the process is resumed. It reports whether the suspend took effect.
//
// Two things here are deliberate, and both came out of a session capture where
// Ctrl-Z appeared to need pressing twice.
//
// It signals the process *group* (pid 0) rather than just this process, because
// that is what the terminal driver does when it sees the suspend character, and
// emulating a key ought to emulate what the key does. It was expected to also
// keep a child alive past its tool call from running on while its parent was
// stopped — but that could not be demonstrated: whenever such a child exists,
// readline is not the thing reading the key, so the kernel has already signalled
// the whole group. Treat this as fidelity to the terminal, not as a fix for an
// observed bug.
//
// And it does not wait forever. That half is a fix for something observed. The
// previous version blocked on SIGCONT
// unconditionally, so a SIGTSTP that failed to stop anything turned into a hung
// harness: the terminal had already been put back into cooked mode by the
// caller, nothing was reading stdin, and the user's typing was echoed by the
// kernel — which looks exactly like a working prompt that has stopped
// accepting input. A suspend that does not take should cost a line of output,
// not the session.
func SuspendProcess() bool {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGCONT)
	defer stop()

	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		return false
	}

	timer := time.NewTimer(suspendGrace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		// A stopped process runs no goroutines, so if we really were stopped
		// the SIGCONT that woke us is already recorded by the time anything
		// here runs — but the timer will also have expired during the stop,
		// and a select picks at random among ready cases. Ask the context
		// directly rather than trusting which case won.
		return ctx.Err() != nil
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

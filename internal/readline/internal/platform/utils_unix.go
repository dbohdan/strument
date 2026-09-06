//go:build aix || darwin || dragonfly || freebsd || (linux && !appengine) || netbsd || openbsd || os400 || solaris || zos

package platform

import (
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

// suspendObserved is how much wall-clock time has to pass across the kill for
// SuspendProcess to conclude that the process really was stopped.
//
// The two outcomes are orders of magnitude apart, so the threshold does not
// have to be judged finely. A signal that stops nothing returns in
// microseconds; a stop that is resumed by hand lasts as long as it takes to
// type `fg`, and even a scripted `kill -CONT` costs a process spawn. Twenty
// milliseconds sits between the two with room on both sides.
const suspendObserved = 20 * time.Millisecond

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
// And it does not wait for a SIGCONT to tell it the stop happened. That was the
// first attempt and it was measuring the wrong thing: a signal sent to one's own
// process group is delivered before the kill returns, so the stop *and* the
// resume are both over by the time the next line runs — the SIGCONT has already
// been spent getting us here, and waiting for another one can only time out. A
// capture showed exactly that: the suspend worked, and the harness then
// announced that it had not.
//
// Elapsed time across the kill measures the thing directly. It is also what
// makes a failed suspend cheap: nothing blocks, so the caller restores raw mode
// either way. The old version blocked on SIGCONT unconditionally, and a SIGTSTP
// that stopped nothing turned into a hung harness — terminal back in cooked
// mode, nothing reading stdin, the user's typing echoed by the kernel, which
// looks exactly like a working prompt that has quietly stopped accepting input.
func SuspendProcess() bool {
	started := time.Now()
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		return false
	}
	return time.Since(started) >= suspendObserved
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

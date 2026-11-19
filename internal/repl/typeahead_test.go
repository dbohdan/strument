//go:build linux || darwin

package repl

import (
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"dbohdan.com/strument/internal/coder"
)

// A key pressed during a turn, before a question exists, must not answer it.
// A stray Enter while the model was still thinking approved the shell
// command it then proposed: the terminal held the Enter, and the confirm
// prompt read it as "yes". Through a real pty, with the Enter already queued
// when the question is asked, the answer has to come from what is typed
// after.
func TestConfirmIgnoresTypeahead(t *testing.T) {
	for _, mode := range []string{"cooked", "raw"} {
		t.Run(mode, func(t *testing.T) {
			ptmx, tty, err := pty.Open()
			if err != nil {
				t.Skipf("no pty available: %v", err)
			}
			defer ptmx.Close()
			defer tty.Close()
			attrs, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlReadTermios)
			if err != nil {
				t.Fatal(err)
			}
			// Cooked is the terminal between prompts, where a stray key waits
			// as a line; raw is how the other pty tests run.
			if mode == "raw" {
				attrs.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
			} else {
				attrs.Lflag &^= unix.ECHO
			}
			if err := unix.IoctlSetTermios(int(tty.Fd()), ioctlWriteTermios, attrs); err != nil {
				t.Fatal(err)
			}

			model := testModel()
			out := &syncBuffer{}
			r, err := New(Options{
				Coder:      coder.New(t.TempDir(), model),
				Config:     testConfig(model),
				ModelAlias: "test",
				Stdin:      tty,
				Stdout:     tty,
				Stderr:     tty,
				IsTerminal: func() bool { return true },
				MakeRaw:    func() error { return nil },
				ExitRaw:    func() error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := ptmx.Read(buf)
					if n > 0 {
						_, _ = out.Write(buf[:n])
						if strings.Contains(string(buf[:n]), "\x1b[6n") {
							_, _ = ptmx.WriteString("\x1b[1;1R")
						}
					}
					if err != nil {
						return
					}
				}
			}()

			// The stray Enter, typed before the question.
			_, _ = ptmx.WriteString("\r")
			time.Sleep(100 * time.Millisecond)
			go func() {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && !strings.Contains(out.String(), "Run shell command?") {
					time.Sleep(10 * time.Millisecond)
				}
				_, _ = ptmx.WriteString("n\r")
			}()
			res := r.Confirmer().Confirm(coder.ConfirmRequest{
				Prompt: "Run shell command?", Command: "echo approved-by-nobody", Purpose: "probe", Grant: coder.GrantBash,
			})
			if res.Yes || res.Always {
				t.Errorf("an Enter typed before the question approved it:\n%q", out.String())
			}
			if !strings.Contains(out.String(), "Discarded what was typed before this question appeared") {
				t.Errorf("the discarded input was not mentioned:\n%q", out.String())
			}
		})
	}
}

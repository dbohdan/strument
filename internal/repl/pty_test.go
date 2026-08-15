//go:build linux || darwin

package repl

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"dbohdan.com/strument/internal/coder"
)

// TestInteractivePty drives the REPL through a real pty in readline's
// interactive mode: prompt, a slash command, a rendered turn, and the
// double-Ctrl-C exit. The master side answers cursor-position queries
// (ESC[6n) like a terminal would, since readline blocks on the response.
func TestInteractivePty(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	// Raw slave: no echo, no canonical buffering, and ^C stays a byte for
	// readline instead of becoming a line-discipline signal.
	attrs, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlReadTermios)
	if err != nil {
		t.Fatal(err)
	}
	attrs.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	if err := unix.IoctlSetTermios(int(tty.Fd()), ioctlWriteTermios, attrs); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = answerStub("plain **strong**\n")

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		Color:      true,
		Stdin:      tty,
		Stdout:     tty,
		Stderr:     tty,
		IsTerminal: func() bool { return true },
		MakeRaw:    func() error { return nil }, // the slave is already raw
		ExitRaw:    func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cdr.Confirm = coder.AutoConfirmer{Fallback: r.Confirmer()}

	// Terminal emulation: collect output, answer cursor queries.
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

	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()

	expect := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(out.String(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q in:\n%q", want, out.String())
	}

	expect("> ")
	_, _ = ptmx.WriteString("/ls\r")
	expect("No files pinned in this session.")

	_, _ = ptmx.WriteString("hi\r")
	expect("plain ")
	expect("\x1b[1mstrong") // live render styles **strong** as bold

	_, _ = ptmx.WriteString("\x03")
	expect("^C again to exit")
	_, _ = ptmx.WriteString("\x03")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("REPL did not exit on the ^C chord")
	}
}

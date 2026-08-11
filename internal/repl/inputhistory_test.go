//go:build linux || darwin

package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"dbohdan.com/strument/internal/coder"
)

// TestInputHistoryKeepsCommandsDropsExit drives a real pty session and
// checks that substantive lines (a prompt, an /ask command) are recorded in
// the input-history file while the session-ender /exit is not.
func TestInputHistoryKeepsCommandsDropsExit(t *testing.T) {
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
	attrs.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	if err := unix.IoctlSetTermios(int(tty.Fd()), ioctlWriteTermios, attrs); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = answerStub("ok\n")
	histFile := filepath.Join(t.TempDir(), "input-history")

	r, err := New(Options{
		Coder:       cdr,
		Config:      testConfig(model),
		ModelAlias:  "test",
		HistoryFile: histFile,
		Stdin:       tty,
		Stdout:      tty,
		Stderr:      tty,
		IsTerminal:  func() bool { return true },
		MakeRaw:     func() error { return nil },
		ExitRaw:     func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cdr.Confirm = coder.AutoConfirmer{Fallback: r.Confirmer()}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 && strings.Contains(string(buf[:n]), "\x1b[6n") {
				_, _ = ptmx.WriteString("\x1b[1;1R")
			}
			if err != nil {
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() { _ = r.Run(context.Background()); close(done) }()

	send := func(s string) {
		_, _ = ptmx.WriteString(s + "\r")
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	send("/ask what does this do?")
	send("just a plain message")
	send("/exit")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("REPL did not exit")
	}

	data, err := os.ReadFile(histFile)
	if err != nil {
		t.Fatalf("input-history not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "/ask what does this do?") {
		t.Errorf("/ask command was not saved to history:\n%q", got)
	}
	if !strings.Contains(got, "just a plain message") {
		t.Errorf("plain message was not saved to history:\n%q", got)
	}
	if strings.Contains(got, "/exit") {
		t.Errorf("/exit should not be saved to history:\n%q", got)
	}
}

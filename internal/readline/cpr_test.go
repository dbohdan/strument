package readline

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is the terminal's output as the test reads it while readline
// writes it from another goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A terminal that never answers the cursor position query used to leave the
// prompt undrawn forever, with Ctrl+C only buffered behind the wait: a user
// saw an approval question never appear and had to kill the process. The
// prompt now draws once the wait is bounded, the keys typed meanwhile are
// read in order, and a reply that turns up late is not taken for input.
func TestPromptDrawsWhenCursorReportNeverComes(t *testing.T) {
	// The console call is about the process's real console, which a pipe is
	// not; on Windows without one it fails before the code under test runs.
	defer func(f func() error) { enableANSI = f }(enableANSI)
	enableANSI = func() error { return nil }

	in, feed := io.Pipe()
	out := &lockedBuffer{}
	rl, err := NewFromConfig(&Config{
		Prompt:             "question? ",
		Stdin:              in,
		Stdout:             out,
		FuncIsTerminal:     func() bool { return true },
		FuncMakeRaw:        func() error { return nil },
		FuncExitRaw:        func() error { return nil },
		FuncGetSize:        func() (int, int) { return 80, 24 },
		FuncOnWidthChanged: func(func()) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	type answer struct {
		line string
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		line, err := rl.ReadLine()
		got <- answer{line, err}
	}()

	deadline := time.Now().Add(cprTimeout + 3*time.Second)
	for !strings.Contains(out.String(), "question? ") {
		if time.Now().After(deadline) {
			t.Fatalf("the prompt was not drawn without a cursor report; output %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "\x1b[6n") {
		t.Fatalf("no cursor position query was sent; output %q", out.String())
	}

	go func() { _, _ = feed.Write([]byte("y\x1b[5;1Res\r")) }()
	select {
	case a := <-got:
		if a.err != nil || a.line != "yes" {
			t.Errorf("ReadLine = %q, %v; want \"yes\", nil", a.line, a.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadLine did not return after Enter")
	}
}

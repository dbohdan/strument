//go:build unix

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A job left running in the background is stopped when the command returns,
// with a deadline or without one. Without, it used to live until the turn
// ended.
func TestBackgroundJobStopsWhenTheCommandReturns(t *testing.T) {
	unixOnly(t)
	for _, timeout := range []time.Duration{0, -1} {
		root := t.TempDir()
		c := &Coder{Out: &captureOut{}, Root: root, ShellTimeout: timeout}
		// The interpreter's $! is a job id, not a pid, so the job reports its
		// own, and the command waits until it has.
		c.runAndShow(context.Background(),
			"sh -c 'echo $$ > pid; exec sleep 30' & while [ ! -s pid ]; do sleep 0.05; done", 0)
		raw, err := os.ReadFile(filepath.Join(root, "pid"))
		pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || perr != nil {
			t.Fatalf("no pid: %v %v %q", err, perr, raw)
		}
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("shell_timeout %v: the background job %d outlived its command", timeout, pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

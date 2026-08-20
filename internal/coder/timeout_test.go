package coder

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/config"
)

// unixOnly skips a test that needs `sleep`, which Windows does not have as a
// command the interpreter can reach. shell_test.go makes the same distinction
// inline for interpreter constructs.
func unixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a Unix `sleep`")
	}
}

// TestShellTimeoutResolution pins the two zeroes, which mean opposite things at
// the two layers this setting crosses.
func TestShellTimeoutResolution(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{"unset takes the default", 0, defaultShellTimeout},
		{"a value is used as given", 30 * time.Second, 30 * time.Second},
		{"negative is no deadline", -1 * time.Second, -1 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Coder{ShellTimeout: tc.set}
			if got := c.shellTimeout(); got != tc.want {
				t.Errorf("shellTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestShellTimeoutStopsACommand exercises the deadline through the real
// interpreter, and pins that the model is told the harness did it. A command
// that was killed at the deadline and one that exited on its own are otherwise
// indistinguishable in a tool result, and the obvious next move after an
// unexplained failure is to change the code.
func TestShellTimeoutStopsACommand(t *testing.T) {
	unixOnly(t)
	c := &Coder{
		Out:          &captureOut{},
		Root:         t.TempDir(),
		ShellTimeout: 150 * time.Millisecond,
	}

	start := time.Now()
	exit, out := c.runAndShow(context.Background(), "sleep 30")
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("the command ran for %s; the deadline did not fire", elapsed)
	}
	if exit == 0 {
		t.Error("a command stopped by the deadline reported success")
	}
	if !strings.Contains(out, "shell_timeout") {
		t.Errorf("the result does not say the harness stopped it:\n%s", out)
	}
}

// TestCtrlCIsNotReportedAsATimeout: a cancelled context is the user's decision
// and needs no explaining back to them, so only DeadlineExceeded is annotated.
func TestCtrlCIsNotReportedAsATimeout(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir(), ShellTimeout: time.Minute}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, out := c.runAndShow(ctx, "sleep 30")

	if strings.Contains(out, "shell_timeout") {
		t.Errorf("an interrupt was reported as a timeout:\n%s", out)
	}
}

// TestNoDeadlineWhenDisabled: `shell_timeout = 0` really means no limit, not a
// zero-length one, which is the failure a naive mapping would produce.
func TestNoDeadlineWhenDisabled(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir(), ShellTimeout: -1}

	exit, out := c.runAndShow(context.Background(), "echo alive")

	if exit != 0 || !strings.Contains(out, "alive") {
		t.Errorf("exit=%d out=%q; a disabled timeout stopped the command", exit, out)
	}
}

// TestCheckTimeoutStopsACheck covers the other model-caused execution path.
// runCheck reaches exec.CommandContext directly rather than going through
// CommandRunner, so it needs its own deadline and its own test — a check that
// waits forever hangs the session exactly as a bash block would.
func TestCheckTimeoutStopsACheck(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir(), ShellTimeout: 150 * time.Millisecond}

	start := time.Now()
	exit, out := c.runCheck(context.Background(), config.Check{Name: "slow", Argv: []string{"sleep", "30"}})

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the check ran for %s; the deadline did not fire", elapsed)
	}
	if exit == 0 {
		t.Error("a check stopped by the deadline reported success")
	}
	if !strings.Contains(out, "shell_timeout") {
		t.Errorf("the result does not say the harness stopped it:\n%s", out)
	}
}

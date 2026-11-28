package coder

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A process a command leaves holding its output does not hold the call: the
// call returns once the command's own process is done, says why the rest is
// not shown, and keeps what was printed. `sh -c '(sleep 20) & …'` is the shape
// that held a session for hours: the background subshell keeps the pipe.
func TestALeftoverProcessDoesNotHoldTheCall(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir()}
	start := time.Now()
	code, out := c.runAndShow(context.Background(), `sh -c '(sleep 20) & echo started'`, 0)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the call took %s: it waited for the leftover process", took)
	}
	if code != 0 || !strings.Contains(out, "started") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "outlived it and still holds its output") {
		t.Errorf("no note on the process left behind:\n%s", out)
	}
}

// The timeout bounds the call even then. The result used to say the command
// was stopped at the limit and arrive when the leftover process exited.
func TestTheTimeoutBoundsACallWithALeftoverProcess(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir()}
	start := time.Now()
	_, out := c.runAndShow(context.Background(), `sh -c '(sleep 30) & sleep 25'`, 2*time.Second)
	if took := time.Since(start); took > 12*time.Second {
		t.Fatalf("a 2s limit took %s:\n%s", took, out)
	}
}

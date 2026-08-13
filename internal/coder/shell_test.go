package coder

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A tool-invoked command has no user at a keyboard, so its stdin is empty.
//
// This passes under both the current wiring and the self-referential one it
// replaced, so it is a guard rather than a regression test: the interpreter was
// given the output buffer as stdin as well as stdout, which a standalone
// reproduction turns into a command reading its own output, and which does not
// reproduce here for reasons I could not isolate. Pinning the behavior is worth
// more than pinning the explanation.
func TestShellStdinIsEmpty(t *testing.T) {
	code, out, err := PipeRunner{}.Run(context.Background(), "echo one; echo two; cat -n", t.TempDir())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if out != "one\ntwo\n" {
		t.Errorf("output = %q, want %q — stdin is reading the output buffer", out, "one\ntwo\n")
	}
}

// The interpreter is bash-compatible where it counts; these are the shapes a
// model actually emits, checked against the real thing in a differential probe
// (25 constructs, no divergence) and pinned here so a dependency bump cannot
// quietly regress them.
func TestShellRunsOrdinaryBashConstructs(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ block, want string }{
		{"echo hello | tr a-z A-Z", "HELLO\n"},
		{"x=3; echo $((x * 7))", "21\n"},
		{"for i in 1 2; do echo n$i; done", "n1\nn2\n"},
		{"echo \"[$(echo inner)]\"", "[inner]\n"},
		{"v=hello; echo ${v^^} ${#v}", "HELLO 5\n"},
		{"arr=(x y z); echo ${arr[1]} ${#arr[@]}", "y 3\n"},
		{"[[ abc == a* ]] && echo yes", "yes\n"},
		{"cat <(echo psub)", "psub\n"},
		{"printf '%s-%03d\\n' x 7", "x-007\n"},
	} {
		code, out, err := PipeRunner{}.Run(context.Background(), tc.block, dir)
		if err != nil || code != 0 {
			t.Errorf("%q: code=%d err=%v", tc.block, code, err)
			continue
		}
		if out != tc.want {
			t.Errorf("%q = %q, want %q", tc.block, out, tc.want)
		}
	}
}

// A non-zero exit is a value, not an error: the model needs the status to know
// its check failed, and an error here would look like the harness broke.
func TestShellExitStatusIsNotAnError(t *testing.T) {
	code, _, err := PipeRunner{}.Run(context.Background(), "exit 3", t.TempDir())
	if err != nil {
		t.Fatalf("a failing command should not error: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

// Cancellation stops a long command rather than waiting it out, and whatever it
// printed first still comes back — an interrupted run should not lose the
// output that explains why it was interrupted.
func TestShellCancellationKeepsPartialOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	code, out, _ := PipeRunner{}.Run(ctx, "echo before; sleep 5; echo after", t.TempDir())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancellation did not stop the command: took %v", elapsed)
	}
	if code == 0 {
		t.Error("a cancelled command should not report success")
	}
	if !strings.Contains(out, "before") {
		t.Errorf("output printed before the cancel was lost: %q", out)
	}
	if strings.Contains(out, "after") {
		t.Errorf("the command kept running past the cancel: %q", out)
	}
}

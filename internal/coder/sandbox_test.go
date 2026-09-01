package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRequiredSandboxRefusesModelCommands pins the distinction the whole
// availability design turns on: a kernel without Landlock stops the *model*
// from running commands, not Strument from starting.
func TestRequiredSandboxRefusesModelCommands(t *testing.T) {
	out := &captureOut{}
	c := &Coder{
		Out:     out,
		Root:    t.TempDir(),
		Sandbox: SandboxState{Required: true, Active: false, Unavailable: "this kernel has no Landlock"},
	}

	exit, result := c.runAndShow(context.Background(), "touch canary", 0)

	if exit == 0 {
		t.Error("a refused command reported success")
	}
	// The model has to learn what happened, not just that it failed.
	for _, want := range []string{"requires a sandbox", "/run", "sandbox = \"\""} {
		if !strings.Contains(result, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, result)
		}
	}
	if _, err := os.Stat(filepath.Join(c.Root, "canary")); err == nil {
		t.Error("the command ran anyway")
	}
}

// TestUnsandboxedSessionStillRuns: with no sandbox required, nothing changes.
func TestUnsandboxedSessionStillRuns(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir()}

	if exit, out := c.runAndShow(context.Background(), "echo ran", 0); exit != 0 || !strings.Contains(out, "ran") {
		t.Errorf("exit=%d out=%q; an unsandboxed session was blocked", exit, out)
	}
}

// TestActiveSandboxDoesNotBlock: an enforcing sandbox permits commands. It is
// the *absent* one that refuses.
func TestActiveSandboxDoesNotBlock(t *testing.T) {
	unixOnly(t)
	c := &Coder{
		Out:     &captureOut{},
		Root:    t.TempDir(),
		Sandbox: SandboxState{Required: true, Active: true, Writable: []string{"/w"}},
	}

	if exit, out := c.runAndShow(context.Background(), "echo ran", 0); exit != 0 || !strings.Contains(out, "ran") {
		t.Errorf("exit=%d out=%q; an active sandbox blocked a command", exit, out)
	}
}

// TestDeniedHintIsAppended: the hint reaches the model, because an unexplained
// permission error is what a coding model tries to fix by editing code.
func TestDeniedHintIsAppended(t *testing.T) {
	unixOnly(t)
	c := &Coder{
		Out:     &captureOut{},
		Root:    t.TempDir(),
		Sandbox: SandboxState{Required: true, Active: true, Writable: []string{"/project", "/tmp"}},
	}

	_, out := c.runAndShow(context.Background(), "echo 'touch: cannot touch: Permission denied' >&2; exit 1", 0)

	if !strings.Contains(out, "sandbox") || !strings.Contains(out, "/project") {
		t.Errorf("no sandbox hint on a denied command:\n%s", out)
	}
	if !strings.Contains(out, "sandbox_write") {
		t.Errorf("the hint does not say how to fix it:\n%s", out)
	}
}

// TestNoHintOnAnOrdinaryFailure: a compile error must not be blamed on the
// sandbox. Crying wolf here is worse than saying nothing, because the hint
// tells the model *not* to fix the code.
func TestNoHintOnAnOrdinaryFailure(t *testing.T) {
	unixOnly(t)
	c := &Coder{
		Out:     &captureOut{},
		Root:    t.TempDir(),
		Sandbox: SandboxState{Required: true, Active: true, Writable: []string{"/project"}},
	}

	_, out := c.runAndShow(context.Background(), "echo 'syntax error near unexpected token' >&2; exit 2", 0)

	if strings.Contains(out, "sandbox") {
		t.Errorf("an ordinary failure was blamed on the sandbox:\n%s", out)
	}
}

// TestLooksDeniedCatchesEXDEV is the case a live probe caught and reasoning
// would have missed. A denied rename between directories is EXDEV, not EACCES:
// it reads as "invalid cross-device link", os.IsPermission is false for it, and
// it is the likeliest breakage under a policy whose writable roots are
// directories.
func TestLooksDenied(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
		why    string
	}{
		{"mv: cannot move 'a' to 'b': Permission denied", true, "the ordinary EACCES write denial"},
		{"rename a b: invalid cross-device link", true, "EXDEV, which os.IsPermission does not catch"},
		{"open /etc/x: operation not permitted", true, "EPERM"},
		{"./main.go:4:2: undefined: foo", false, "a compile error is not the sandbox"},
		{"FAIL: TestThing (0.01s)", false, "a test failure is not the sandbox"},
		{"", false, "no output says nothing"},
	} {
		if got := looksDenied(tc.output); got != tc.want {
			t.Errorf("looksDenied(%q) = %v, want %v — %s", tc.output, got, tc.want, tc.why)
		}
	}
}

// TestAllTurnOfferedOnlyUnderASandbox pins the condition that licenses the
// affordance rather than just its presence.
//
// "a = all this turn" was removed on the argument that approving an unseen
// command because the last one was fine is the reflex the prompt exists to
// interrupt. That argument is about consequences, and a sandbox bounds
// consequences — so the offer comes back exactly when a bad approval can no
// longer write outside the project, and not before.
func TestAllTurnOfferedOnlyUnderASandbox(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sandbox   SandboxState
		wantGroup bool
	}{
		{"active", SandboxState{Required: true, Active: true}, true},
		{"off", SandboxState{}, false},
		{"required but unavailable", SandboxState{Required: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := &recordingConfirmer{}
			c := &Coder{
				Out:                  &captureOut{},
				Root:                 t.TempDir(),
				Sandbox:              tc.sandbox,
				Confirm:              rc,
				SuggestShellCommands: true,
			}
			c.turnAutoApprove = map[string]bool{}

			_ = c.runShellTool(context.Background(), toolCommand{command: "echo hi", purpose: "say hi"})

			if tc.sandbox.blocksExecution() {
				// Refused before the prompt: asking someone to approve a
				// command that is refused either way teaches them the prompt
				// does not matter.
				if len(rc.got) != 0 {
					t.Errorf("the user was asked about a command that could not run: %+v", rc.got)
				}
				return
			}
			if len(rc.got) != 1 {
				t.Fatalf("expected exactly one confirmation, got %d", len(rc.got))
			}
			if got := rc.got[0].Group != ""; got != tc.wantGroup {
				t.Errorf("Group=%q, want offered=%v — the offer must follow the sandbox, not the other way round",
					rc.got[0].Group, tc.wantGroup)
			}
		})
	}
}

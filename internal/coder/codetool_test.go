package coder

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The code tool's tests. Monty's own behavior is pinned here at the level the
// model sees — the returned value and the error text — because that is the
// contract the tool description promises. The security claims (no filesystem,
// no network) are tested, not assumed: each program below was verified to
// fail, and stays red if a Monty upgrade ever makes it pass.

func TestCodeArithmetic(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, tc := range []struct {
		code, want string
	}{
		{"1 + 2", "3"},
		{"2.5 * 4", "10"},
		{"round(3.14159, 2)", "3.14"},
		{"sum(x * x for x in range(10))", "285"},
		{"f'{42:08d}'", "00000042"},
		{"'5'.zfill(3)", "005"},
		{"import math\nmath.sqrt(1764)", "42"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: tc.code})
			if got != tc.want {
				t.Errorf("code %q: got %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// TestCodeRoundExists pins the probe result that decided the tool
// description: round() is available. It is the single most likely call in
// model-written number formatting, and its absence would have to be said so.
func TestCodeRoundExists(t *testing.T) {
	c, _ := observeEnv(t, nil)
	got := c.runCode(context.Background(), codeCall{code: "round(3.7)"})
	if got != "4" {
		t.Errorf("round(3.7): got %q, want \"4\" — the tool description says round() is available", got)
	}
}

// TestCodeInfiniteLoopTerminates is the duration limit's whole point: a
// runaway loop ends on the limit rather than hanging the turn.
func TestCodeInfiniteLoopTerminates(t *testing.T) {
	c, _ := observeEnv(t, nil)

	start := time.Now()
	got := c.runCode(context.Background(), codeCall{code: "while True: pass"})
	elapsed := time.Since(start)

	if !strings.Contains(got, "failed") && !strings.Contains(got, "limit") {
		t.Errorf("an infinite loop must return an error text, got: %q", got)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the duration limit did not fire; ran %v", elapsed)
	}
}

// TestCodeMemoryBombTerminates is the memory limit: an allocation loop ends on
// the limit rather than taking the process with it.
func TestCodeMemoryBombTerminates(t *testing.T) {
	c, _ := observeEnv(t, nil)

	got := c.runCode(context.Background(), codeCall{code: "x = []\nwhile True:\n    x = x + [0] * 1000"})
	if !strings.Contains(got, "failed") && !strings.Contains(got, "limit") {
		t.Errorf("a memory bomb must return an error text, got: %q", got)
	}
}

// TestCodeUnsupportedSyntaxIsUsefulError is the contract the tool description
// leans on: a wall returns Monty's own error, which names the construct, and
// the turn survives it.
func TestCodeUnsupportedSyntaxIsUsefulError(t *testing.T) {
	c, _ := observeEnv(t, nil)

	got := c.runCode(context.Background(), codeCall{code: "class A: pass"})
	if !strings.Contains(got, "class definitions") {
		t.Errorf("a class definition must name itself in the error, got: %q", got)
	}
}

// TestCodeToolOfferedInAskMode: computing mutates nothing, so a discussion
// turn gets the calculator too.
func TestCodeToolOfferedInAskMode(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.editFormat = "ask"

	found := false
	for _, d := range c.toolDefs() {
		if d.Name == toolCode {
			found = true
		}
	}
	if !found {
		t.Error("the code tool must be offered in ask mode")
	}

	c.editFormat = "tool"
	found = false
	for _, d := range c.toolDefs() {
		if d.Name == toolCode {
			found = true
		}
	}
	if !found {
		t.Error("the code tool must be offered in tool mode")
	}
}

// TestCodeDescriptionNamesTheLimits checks the description stays honest about
// the subset. A line that stops describing a real wall is a lie to the model;
// each substring here corresponds to a probe in the tests below.
func TestCodeDescriptionNamesTheLimits(t *testing.T) {
	desc := codeTool().Description
	for _, want := range []string{"class", "with", "match", "math", "re", "datetime", "json"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description must mention %q:\n%s", want, desc)
		}
	}
}

// TestCodeNoFilesystemAccess is the security claim, tested not assumed. Monty
// has no `open` and routes os/pathlib through an OsCallFunc this tool never
// registers, so the calls must fail. If a Monty upgrade makes any of these
// succeed, that upgrade must not ship.
func TestCodeNoFilesystemAccess(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, code := range []string{
		"open('/etc/passwd')",
		"import os\nos.getcwd()",
		"import os\nos.listdir('/')",
		"import os\nos.getenv('HOME')",
		"import pathlib\npathlib.Path('/').exists()",
	} {
		t.Run(code, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: code})
			if !strings.Contains(got, "The program failed") {
				t.Errorf("filesystem access must fail and did not:\n%s", got)
			}
		})
	}
}

// TestCodeNoNetworkAccess: the same claim for the network. There is no socket
// module and no OsCallFunc to route anything through.
func TestCodeNoNetworkAccess(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, code := range []string{
		"import socket\nsocket.socket()",
		"import urllib.request\nurllib.request.urlopen('http://localhost:1')",
	} {
		t.Run(code, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: code})
			if !strings.Contains(got, "The program failed") {
				t.Errorf("network access must fail and did not:\n%s", got)
			}
		})
	}
}

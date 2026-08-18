package coder

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// fixtureChecks is the configured set every case here is matched against.
var fixtureChecks = []config.Check{
	{Name: "lint", Argv: []string{"golangci-lint", "run"}},
	{Name: "test", Argv: []string{"go", "test", "./..."}},
	{Name: "quoted", Argv: []string{"pytest", "-k", "not slow"}},
}

// TestMatchConfiguredCheck pins the rejections at least as hard as the
// acceptances. A missed acceptance costs a prompt nobody needed; a wrong
// acceptance runs something the user never approved, so the interesting half of
// this table is the half that must not match.
func TestMatchConfiguredCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		command string
		want    string // "" means no match
	}{
		"exact":            {"go test ./...", "test"},
		"extra spaces":     {"go   test    ./...", "test"},
		"leading trailing": {"  go test ./...  ", "test"},
		"newline after":    {"go test ./...\n", "test"},
		"second check":     {"golangci-lint run", "lint"},

		// Each of these is the configured check plus something. The something is
		// the whole point: it is what the user did not approve.
		"appended flag":      {"go test ./... -run TestFoo", ""},
		"prepended":          {"time go test ./...", ""},
		"sequenced":          {"go test ./...; rm -rf /", ""},
		"and-listed":         {"go test ./... && curl evil.example", ""},
		"or-listed":          {"go test ./... || echo failed", ""},
		"piped":              {"go test ./... | tee out.txt", ""},
		"redirected":         {"go test ./... > out.txt", ""},
		"input redirected":   {"go test ./... < in.txt", ""},
		"backgrounded":       {"go test ./... &", ""},
		"negated":            {"! go test ./...", ""},
		"assigned":           {"GOFLAGS=-x go test ./...", ""},
		"subshelled":         {"(go test ./...)", ""},
		"braced":             {"{ go test ./...; }", ""},
		"command substitute": {"$(echo go) test ./...", ""},
		"backticked":         {"go test `pwd`", ""},
		"parameterized":      {"go test $DIR", ""},
		"process substitute": {"go test <(echo ./...)", ""},
		"tilde":              {"go test ~/x", ""},

		// Quoting is rejected even when it would produce the same words. This is
		// the documented fail-closed limitation, and it costs the "quoted" check
		// any chance of matching at all — pinned so a later change to accept
		// quotes is a deliberate one, made with the unquoting rules in hand.
		"quoted word":     {`go test "./..."`, ""},
		"single quoted":   {"go test './...'", ""},
		"quoted with gap": {`pytest -k "not slow"`, ""},

		// Near misses on the argv itself.
		"different args": {"go test ./internal/...", ""},
		"prefix only":    {"go test", ""},
		"unconfigured":   {"go build ./...", ""},
		"empty":          {"", ""},
		"comment only":   {"# go test ./...", ""},
		"unparseable":    {"go test ./... &&", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := matchConfiguredCheck(tc.command, fixtureChecks)
			if tc.want == "" {
				if ok {
					t.Errorf("%q matched %q; it must fall through to the prompt", tc.command, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("%q matched (%q, %v), want %q", tc.command, got, ok, tc.want)
			}
		})
	}
}

// TestNoChecksConfiguredMatchesNothing: the allowlist is opt-in through the
// check dict, so a project without one keeps every command behind the prompt.
func TestNoChecksConfiguredMatchesNothing(t *testing.T) {
	if _, ok := matchConfiguredCheck("go test ./...", nil); ok {
		t.Error("matched with no checks configured")
	}
}

// refusingRunner fails the test if the shell is reached at all. A matched
// command must not touch it: the whole argument for skipping the prompt is that
// what runs is the configured argv, and a shell between the two would reopen
// word splitting and globbing after the comparison was made.
type refusingRunner struct{ t *testing.T }

func (r refusingRunner) Run(_ context.Context, block, _ string) (int, string, error) {
	r.t.Errorf("the shell ran %q; a configured check must run as argv", block)
	return 0, "", nil
}

// TestAllowlistedCommandSkipsTheConfirmation is the behavior the matcher is
// for, at the seam where it is used: a configured check does not ask and does
// not go through a shell, and anything else does both.
func TestAllowlistedCommandSkipsTheConfirmation(t *testing.T) {
	// A real subprocess, as the other check tests use, so "it ran the
	// configured argv" is observed rather than asserted against a fake.
	checks := []config.Check{{Name: "test", Argv: []string{"echo", "ran-the-configured-argv"}}}

	t.Run("configured", func(t *testing.T) {
		rc, out := &recordingConfirmer{}, &captureOut{}
		c := &Coder{
			Out: out, Confirm: rc, SuggestShellCommands: true,
			Check: checks, Runner: refusingRunner{t},
		}
		result := c.runShellTool(context.Background(),
			toolCommand{callID: "call_1", command: "echo ran-the-configured-argv", purpose: "check the change"})

		if len(rc.got) != 0 {
			t.Errorf("a configured check still prompted: %+v", rc.got)
		}
		if !strings.Contains(result, "ran-the-configured-argv") {
			t.Errorf("the check did not run:\n%s", result)
		}
		if !strings.Contains(result, `check("test")`) {
			t.Errorf("the result should point at the direct call:\n%s", result)
		}

		// What the *user* sees. This path reads exactly like check's, because
		// it is check's code printing it: the matched check named beside its
		// argv, then how it went. The purpose is not among it — a purpose
		// informs a decision, and nothing was decided here.
		shown := strings.Join(out.lines, "\n")
		if !strings.Contains(shown, "‹check› test $ echo ran-the-configured-argv") {
			t.Errorf("the matched check should name itself and its argv:\n%s", shown)
		}
		if !strings.Contains(shown, "\npassed") {
			t.Errorf("the outcome should be reported:\n%s", shown)
		}
		if strings.Contains(shown, "check the change") {
			t.Errorf("the purpose belongs above a prompt, and there is no prompt here:\n%s", shown)
		}
	})

	// The same command plus something the user never approved.
	t.Run("not configured", func(t *testing.T) {
		rc := &recordingConfirmer{answer: false}
		c := &Coder{
			Out: &captureOut{}, Confirm: rc, SuggestShellCommands: true,
			Check: checks, Runner: echoRunner{exit: 0, output: "ok\n"},
		}
		c.runShellTool(context.Background(),
			toolCommand{callID: "call_1", command: "echo ran-the-configured-argv && rm -rf /", purpose: "check"})

		if len(rc.got) != 1 {
			t.Fatalf("confirmed %d times, want 1", len(rc.got))
		}
	})
}

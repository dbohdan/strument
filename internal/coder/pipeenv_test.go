package coder

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/envallow"
)

// TestPipeRunnerEnvIsTheAllowlist observes the seam the model actually reaches:
// a block through the real interpreter, with the allowlist applied the way
// runAndShow applies it. Asserted by running `env`, not by re-deriving the
// filter — the interpreter is what decides what the block sees.
func TestPipeRunnerEnvIsTheAllowlist(t *testing.T) {
	t.Setenv("STRUMENT_TEST_SECRET", "sk-leak")
	t.Setenv("STRUMENT_TEST_PLAIN", "ok")
	t.Setenv("STRUMENT_TEST_ALLOWED", "ok2")

	_, out, err := PipeRunner{Env: envallow.Filter(nil, []string{"STRUMENT_TEST_ALLOWED"})}.
		Run(context.Background(), "env", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "STRUMENT_TEST_SECRET") {
		t.Errorf("the secret reached the model-run block:\n%s", out)
	}
	if strings.Contains(out, "STRUMENT_TEST_PLAIN") {
		t.Errorf("a non-allowlisted variable reached the model-run block:\n%s", out)
	}
	if !strings.Contains(out, "STRUMENT_TEST_ALLOWED=ok2") {
		t.Errorf("the env_allow addition did not reach the block:\n%s", out)
	}
	if !strings.Contains(out, "PATH=") {
		t.Errorf("PATH did not reach the block:\n%s", out)
	}
}

// TestPipeRunnerSeedsPWD asserts the fidelity seam directly: a model-run
// block must see PWD naming its own directory, whatever the session's
// environment carried. Both directions matter. An inherited PWD naming
// somewhere else must not survive — a stale value trusted blindly is
// worse than none — and an absent one must appear, because the session's
// process legitimately has none when a non-shell parent started it (the
// state the harness this test suite runs in was started in).
func TestPipeRunnerSeedsPWD(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// The real PATH, not a fixed one: the block runs `env`, which Windows
	// finds only in Git's usr/bin, and "PATH=/usr/bin" names nothing there.
	path := "PATH=" + os.Getenv("PATH")
	stale := envallow.Filter(func() []string {
		return []string{path, "PWD=/stale/session/start"}
	}, nil)
	_, out, err := (PipeRunner{Env: stale}).Run(ctx, "env", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PWD="+dir) {
		t.Errorf("the block does not see PWD naming its directory %q:\n%s", dir, out)
	}
	if strings.Contains(out, "/stale/session/start") {
		t.Errorf("the session's stale PWD reached the block:\n%s", out)
	}

	absent := envallow.Filter(func() []string { return []string{path} }, nil)
	_, out, err = (PipeRunner{Env: absent}).Run(ctx, "env", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PWD="+dir) {
		t.Errorf("a session without PWD gave the block none:\n%s", out)
	}
}

// Every way a block starts a child must pass PWD on, not only the simple
// command the seed test runs. mvdan/sh assigns its own PWD unexported, on
// start and on every cd, and the exported copy from the environment shows
// through only at the top level: a pipeline stage is a background subshell
// that flattens the two, and the unexported one wins. So `env | grep PWD`
// found nothing while `env` found it — the probe a model reaches for first
// was the one case that failed.
func TestPipeRunnerPWDReachesEveryChild(t *testing.T) {
	dir, into := t.TempDir(), t.TempDir()
	env := envallow.Filter(func() []string {
		return []string{"PATH=" + os.Getenv("PATH"), "PWD=/stale/session/start"}
	}, nil)
	for _, tc := range []struct{ block, want string }{
		{"env | grep '^PWD='", dir},
		{"echo \"$(env)\" | grep '^PWD='", dir},
		{fmt.Sprintf("cd %q && env", into), into},
		{fmt.Sprintf("cd %q && env | grep '^PWD='", into), into},
		{fmt.Sprintf("(cd %q; env)", into), into},
	} {
		_, out, err := (PipeRunner{Env: env}).Run(context.Background(), tc.block, dir)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "PWD="+tc.want) {
			t.Errorf("%s: the child does not see PWD naming %q:\n%s", tc.block, tc.want, out)
		}
		if strings.Contains(out, "/stale/session/start") {
			t.Errorf("%s: the session's stale PWD reached the child:\n%s", tc.block, out)
		}
	}
}

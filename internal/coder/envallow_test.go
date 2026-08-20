package coder

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// fixtureEnv stands in for the session's environment: one credential-shaped
// pair, one plausibly needed pair, and representatives of each default class.
var fixtureEnv = []string{
	"PATH=/usr/bin",
	"HOME=/home/u",
	"LANG=C.UTF-8",
	"LC_ALL=C.UTF-8",
	"LC_MESSAGES=C.UTF-8",
	"GOPATH=/home/u/go",
	"GOFLAGS=-mod=readonly",
	"GOOGLE_API_KEY=g-secret",
	"GOOGLE_TOKEN=g-token",
	"TMPDIR=/tmp",
	"CARGO_HOME=/home/u/.cargo",
	"VIRTUAL_ENV=/home/u/venv",
	"OPENROUTER_API_KEY=sk-secret",
	"GITHUB_TOKEN=ghp-secret",
	"MY_SERVICE_TOKEN=also-secret",
	"CI_JOB_TOKEN=x",
	"NPM_TOKEN=npm-secret",
	"DATABASE_URL=postgres://pw@db",
	"TERM=xterm-256color",
	"SOCKS5_SERVER=socks5://127.0.0.1:1080",
	"HTTPS_PROXY=socks5://127.0.0.1:1080",
	"NO_PROXY=localhost,127.0.0.1",
	"FOO=bar",
	"BAR=1",
	"HOME_BACKUP=/home/u/.bak",
}

func fixtureFilter(extra ...string) []string {
	return FilterEnv(func() []string { return fixtureEnv }, extra)
}

// TestFilterEnvDefaults pins the halves asymmetrically, the same way
// TestMatchConfiguredCheck does: a wrongly-passed secret is the expensive
// direction, so the withheld half of the table is the half that must keep
// matching as the default list evolves.
func TestFilterEnvDefaults(t *testing.T) {
	got := fixtureFilter()
	gotNames := envNames(got)

	for _, name := range []string{
		// The classes the defaults exist for.
		"PATH", "HOME", "LANG", "LC_ALL", "LC_MESSAGES", "GOPATH", "GOFLAGS",
		"TMPDIR", "CARGO_HOME",
		"VIRTUAL_ENV", "TERM",
		"SOCKS5_SERVER", "HTTPS_PROXY", "NO_PROXY",
	} {
		if !slices.Contains(gotNames, name) {
			t.Errorf("%s was withheld; builds need it", name)
		}
	}
	for _, name := range []string{
		// Credentials — the whole point of the allowlist.
		"OPENROUTER_API_KEY", "GITHUB_TOKEN", "MY_SERVICE_TOKEN",
		"CI_JOB_TOKEN", "NPM_TOKEN", "DATABASE_URL",
		// GOOGLE_API_KEY / GOOGLE_TOKEN pin that the GO* family is an exact
		// enumeration, not a "GO" prefix match.
		"GOOGLE_API_KEY", "GOOGLE_TOKEN",
		// Not credentials, not on the list either: passing them would be a
		// widening nobody asked for. HOME_BACKUP pins that a prefix of an
		// allowed name is not the allowed name.
		"FOO", "BAR", "HOME_BACKUP",
	} {
		if slices.Contains(gotNames, name) {
			t.Errorf("%s was passed to model-run commands", name)
		}
	}
}

func TestFilterEnvUserAdditions(t *testing.T) {
	// env_allow adds on top; it does not replace the defaults.
	got := envNames(fixtureFilter("FOO"))
	for _, name := range []string{"FOO", "PATH", "HOME"} {
		if !slices.Contains(got, name) {
			t.Errorf("%s missing after env_allow", name)
		}
	}

	// Credential-shaped names pass when explicitly allowed — deliberately, not
	// by oversight: a hard shape filter would push users toward writing tokens
	// to files (see the comment on defaultEnvAllowNames). What is pinned here
	// is that the deliberate addition works; what guards it is that it must be
	// written down at all.
	if !slices.Contains(envNames(fixtureFilter("GITHUB_TOKEN")), "GITHUB_TOKEN") {
		t.Error("an explicit env_allow entry did not pass")
	}

	// A trailing underscore does not become a prefix match: allowing "FOO_"
	// must not admit FOO_BAR, or config authors would widen permissions past
	// what they wrote.
	if slices.Contains(envNames(fixtureFilter("FOO_")), "FOO") {
		t.Error("env_allow matched by prefix")
	}
}

// TestFilterEnvNeverReturnsNil pins the failure direction of the filter. Both
// consumers read a nil environment as "inherit everything" — exec.Cmd.Env by
// documented contract, PipeRunner.Env by its zero-value rule — so the one case
// where the filter passes nothing must not be the case where it stops
// filtering.
func TestFilterEnvNeverReturnsNil(t *testing.T) {
	got := FilterEnv(func() []string { return []string{"OPENROUTER_API_KEY=sk-secret"} }, nil)
	if got == nil {
		t.Fatal("a fully filtered environment came back nil, which both call sites read as inherit-everything")
	}
	if len(got) != 0 {
		t.Errorf("nothing should have passed, got %v", got)
	}
}

// TestPipeRunnerEnvIsTheAllowlist observes the seam the model actually reaches:
// a block through the real interpreter, with the allowlist applied the way
// runAndShow applies it. Asserted by running `env`, not by re-deriving the
// filter — the interpreter is what decides what the block sees.
func TestPipeRunnerEnvIsTheAllowlist(t *testing.T) {
	t.Setenv("STRUMENT_TEST_SECRET", "sk-leak")
	t.Setenv("STRUMENT_TEST_PLAIN", "ok")
	t.Setenv("STRUMENT_TEST_ALLOWED", "ok2")

	_, out, err := PipeRunner{Env: FilterEnv(nil, []string{"STRUMENT_TEST_ALLOWED"})}.
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

func envNames(pairs []string) []string {
	out := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		name, _, _ := strings.Cut(kv, "=")
		out = append(out, name)
	}
	return out
}

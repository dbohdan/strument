package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/gitrepo"
)

// The point of the command: --no-auto-commits becomes a decision you can make
// mid-session.
func TestCommitsTogglesAutoCommits(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Repo = &gitrepo.Repo{}
	cdr.AutoCommits = true

	cmdCommits(context.Background(), r, "off")
	if cdr.AutoCommits {
		t.Error("/commits off left auto-commits on")
	}
	cmdCommits(context.Background(), r, "on")
	if !cdr.AutoCommits {
		t.Error("/commits on left auto-commits off")
	}
	if !strings.Contains(out.String(), "Commits: on") {
		t.Errorf("the new state was not reported:\n%s", out.String())
	}
}

// The deliberate divergence from /raw in Codex CLI and /yolo in Kimi Code, and
// the reason this test exists: bare reports. A command that changes behaviour
// when you typed it to ask about behaviour is the ambiguity the house pattern
// avoids, and /model, /env and /notes all report when given nothing.
func TestCommitsBareDoesNotChangeState(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Repo = &gitrepo.Repo{}

	for _, want := range []bool{true, false} {
		cdr.AutoCommits = want
		cmdCommits(context.Background(), r, "")
		if cdr.AutoCommits != want {
			t.Errorf("bare /commits flipped auto-commits from %v", want)
		}
	}
	if !strings.Contains(out.String(), "Commits:") {
		t.Errorf("bare /commits reported nothing:\n%s", out.String())
	}
}

// What it reports has to be what actually gates a commit, not just the flag it
// sets: "on" while a dry run or a missing repository means nothing will be
// committed is a worse answer than no answer.
func TestCommitsReportsWhatActuallyGatesACommit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(*coder.Coder)
		want    string
		wantNot string
	}{
		{
			name:  "no repository",
			setup: func(c *coder.Coder) { c.Repo = nil; c.AutoCommits = true },
			want:  "git integration is not on",
		},
		{
			name:  "dry run outranks the setting",
			setup: func(c *coder.Coder) { c.Repo = &gitrepo.Repo{}; c.AutoCommits = true; c.DryRun = true },
			want:  "dry run",
		},
		{
			name:    "plainly off",
			setup:   func(c *coder.Coder) { c.Repo = &gitrepo.Repo{}; c.AutoCommits = false },
			want:    "Commits: off",
			wantNot: "dry run",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
			tc.setup(cdr)
			cmdCommits(context.Background(), r, "")
			got := out.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, got)
			}
			if tc.wantNot != "" && strings.Contains(got, tc.wantNot) {
				t.Errorf("did not want %q in:\n%s", tc.wantNot, got)
			}
		})
	}
}

// Turning it on without a repository cannot work: the handle is built at
// startup. Setting a flag that can have no effect is the failure to avoid.
func TestCommitsOnWithoutARepositoryIsRefused(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Repo = nil

	cmdCommits(context.Background(), r, "on")
	if cdr.AutoCommits {
		t.Error("auto-commits was set with no repository to commit to")
	}
	if !strings.Contains(out.String(), "nothing to commit to") {
		t.Errorf("the refusal did not say why:\n%s", out.String())
	}
}

func TestCommitsRejectsAnUnknownWord(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Repo = &gitrepo.Repo{}
	cdr.AutoCommits = true

	cmdCommits(context.Background(), r, "yes")
	if !cdr.AutoCommits {
		t.Error("an unrecognized word changed the setting")
	}
	if !strings.Contains(out.String(), "Usage: /commits") {
		t.Errorf("want the usage line:\n%s", out.String())
	}
}

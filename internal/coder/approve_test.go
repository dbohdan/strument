package coder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/decide"
	"dbohdan.com/strument/internal/record"
)

// fixedDecider answers every command the same way and counts the calls.
type fixedDecider struct {
	p     float64
	err   error
	calls int
}

func (f *fixedDecider) decide(context.Context, decide.Input) (decide.Decision, error) {
	f.calls++
	if f.err != nil {
		return decide.Decision{}, f.err
	}
	return decide.Decision{PSafe: f.p, Model: "laya:en"}, nil
}

// TestApproveModelStandsInForThePromptOnly drives runShell. The model is asked
// only when a prompt would really be shown and the sandbox is enforcing; an
// approval skips the prompt; anything else shows it; and every call leaves a
// decision record with its p(safe).
func TestApproveModelStandsInForThePromptOnly(t *testing.T) {
	unixOnly(t)
	for _, tc := range []struct {
		name       string
		p          float64
		err        error
		sandbox    bool
		granted    bool
		wantCalls  int
		wantPrompt bool
		outcome    string
		line       string // printed, in this order
		note       string // carried to the prompt, shown after the command
	}{
		// The verdict follows the command it is about: an approval under the
		// command shown as the prompt would have shown it, purpose first, a
		// refusal under the command in the prompt.
		{"approved", 0.95, nil, true, false, 1, false, "approved", "‹shell› test\n$ echo hi\n\nApproved by laya:en, p(safe) = 0.95.", ""},
		{"at the threshold", 0.9, nil, true, false, 1, false, "approved", "p(safe) = 0.90", ""},
		{"below", 0.5, nil, true, false, 1, true, "asked", "", "Not approved by laya:en, p(safe) = 0.50 < 0.90."},
		{"failed", 0, errors.New("connection refused"), true, false, 1, true, "failed", "did not answer", ""},
		{"no sandbox", 0.99, nil, false, false, 0, true, "", "", ""},
		{"already granted", 0.99, nil, true, true, 0, false, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCoder(t)
			out := &captureOut{}
			c.Out = out
			rec := &capture{}
			c.Recorder = rec
			rc := &recordingConfirmer{answer: true}
			c.Confirm = rc
			c.SuggestShellCommands = true
			c.Sandbox = SandboxState{Active: tc.sandbox}
			c.Grants = NewGrants(map[string]bool{GrantBash: tc.granted})
			if tc.granted {
				c.Confirm = AutoConfirmer{Granted: c.Grants.Effective, Fallback: rc}
			}
			fd := &fixedDecider{p: tc.p, err: tc.err}
			c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}

			_, ran := c.runShell(context.Background(), toolCommand{command: "echo hi", purpose: "test"})
			if !ran {
				t.Fatal("the command did not run")
			}
			if fd.calls != tc.wantCalls {
				t.Errorf("decision model called %d times, want %d", fd.calls, tc.wantCalls)
			}
			if prompted := len(rc.got) > 0; prompted != tc.wantPrompt {
				t.Errorf("prompted = %v, want %v", prompted, tc.wantPrompt)
			}
			joined := strings.Join(out.lines, "\n")
			if tc.line != "" && !strings.Contains(joined, tc.line) {
				t.Errorf("output lacks %q:\n%s", tc.line, joined)
			}
			if strings.Contains(joined, "Not approved") {
				t.Errorf("the refusal was printed before the prompt showed the command:\n%s", joined)
			}
			if tc.wantPrompt && len(rc.got) > 0 && rc.got[0].Note != tc.note {
				t.Errorf("prompt note = %q, want %q", rc.got[0].Note, tc.note)
			}
			var dec []record.Record
			for _, r := range rec.recs {
				if r.Type == record.KindDecision {
					dec = append(dec, r)
				}
			}
			if tc.outcome == "" {
				if len(dec) != 0 {
					t.Errorf("recorded %d decisions for a call never made", len(dec))
				}
				return
			}
			if len(dec) != 1 || dec[0].Outcome != tc.outcome {
				t.Fatalf("decision records = %+v, want one with outcome %q", dec, tc.outcome)
			}
			if (tc.err == nil) != (dec[0].PSafe != nil) {
				t.Errorf("p_safe recorded = %v; it belongs on an answer and not on a failure", dec[0].PSafe)
			}
		})
	}
}

// TestApproveModelNeverOverridesADecline: below the threshold the prompt
// decides, and a "no" there is a no.
func TestApproveModelNeverOverridesADecline(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	c.Confirm = noConfirmer{}
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	fd := &fixedDecider{p: 0.2}
	c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}
	if _, ran := c.runShell(context.Background(), toolCommand{command: "echo hi", purpose: "test"}); ran {
		t.Error("a declined command ran")
	}
}

// TestApproveModelSkipsLongCommands: a command too long to be read whole is
// asked about without consulting the model, since a model that cut it would
// answer about a prefix and could not say so.
func TestApproveModelSkipsLongCommands(t *testing.T) {
	c := testCoder(t)
	out := &captureOut{}
	c.Out = out
	rc := &recordingConfirmer{answer: false}
	c.Confirm = rc
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	fd := &fixedDecider{p: 1}
	c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}
	long := strings.Repeat("echo ok; ", 100) + "mv release.tgz /var/backups/app/"
	if _, ran := c.runShell(context.Background(), toolCommand{command: long, purpose: "checks"}); ran {
		t.Error("a declined long command ran")
	}
	if fd.calls != 0 || len(rc.got) != 1 {
		t.Errorf("decider calls %d, prompts %d; want 0 and 1", fd.calls, len(rc.got))
	}
	if len(rc.got) == 1 && !strings.Contains(rc.got[0].Note, "more than it may read whole") {
		t.Errorf("the prompt did not say why the model was not asked: note %q", rc.got[0].Note)
	}
}

// TestSecretPathsGoToTheUser: a command naming a secret-shaped file is asked
// about with the path named, even with a decision model that would approve
// anything and an "a" already given this turn.
func TestSecretPathsGoToTheUser(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	rc := &recordingConfirmer{answer: false}
	c.Confirm = rc
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	c.turnAutoApprove["shell"] = true
	fd := &fixedDecider{p: 1}
	c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}
	if _, ran := c.runShell(context.Background(), toolCommand{command: `cat ".e"nv`, purpose: "look"}); ran {
		t.Error("a declined command ran")
	}
	if fd.calls != 0 || len(rc.got) != 1 {
		t.Fatalf("decider calls %d, prompts %d; want 0 and 1", fd.calls, len(rc.got))
	}
	if got := rc.got[0]; !strings.Contains(got.Note, ".env") || got.Group != "" {
		t.Errorf("prompt = %+v; want a note naming .env and no \"a\"", got)
	}
}

// TestDeclinedCommandIsNotAskedAgain: with no one at a terminal, a command the
// classifier declined is declined again without a second call, and the model
// is told it was a classifier and that retrying cannot work. A different
// command still goes to the classifier, and a new turn starts clean.
func TestDeclinedCommandIsNotAskedAgain(t *testing.T) {
	c := testCoder(t)
	c.Out = &captureOut{}
	c.Confirm = unattendedConfirmer{}
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	fd := &fixedDecider{p: 0.08}
	c.Approve = &ApproveModel{Decide: fd.decide, Slug: "laya", Threshold: 0.9}
	run := func(cmd string) string {
		out, ran := c.runShell(context.Background(), toolCommand{command: cmd, purpose: "tidy"})
		if ran {
			t.Fatalf("%s ran", cmd)
		}
		return out
	}

	first := run("rm scratch_test.go")
	if !strings.Contains(first, "classifier") || !strings.Contains(first, "0.08") {
		t.Errorf("first decline does not say a classifier answered: %q", first)
	}
	again := run("rm scratch_test.go")
	if fd.calls != 1 {
		t.Errorf("the classifier was asked %d times; an identical retry should not ask it", fd.calls)
	}
	if !strings.Contains(again, "earlier in this turn") || !strings.Contains(again, "cannot succeed") ||
		!strings.Contains(again, "same answer") {
		t.Errorf("repeat answer = %q", again)
	}
	run("rm other.go")
	if fd.calls != 2 {
		t.Errorf("a different command was not sent to the classifier (calls %d)", fd.calls)
	}
	c.initBeforeMessage()
	run("rm scratch_test.go")
	if fd.calls != 3 {
		t.Errorf("a new turn did not ask again (calls %d)", fd.calls)
	}
}

// TestShellCommandShownOnceInFull: a command is printed whole where the user
// first sees it. After a prompt they answered, which showed it whole, its
// first line stands in for it above the output; approved unseen, it is shown
// whole, purpose first, as the prompt would have shown it.
func TestShellCommandShownOnceInFull(t *testing.T) {
	const command = "cat <<'EOF'\nline one\nline two\nEOF"
	for _, tc := range []struct {
		name    string
		answer  Confirmer
		grant   bool
		want    string
		notWant string
	}{
		{"answered at the prompt", &recordingConfirmer{answer: true}, false,
			"$ cat <<'EOF'  (+3 more lines)", "‹shell›"},
		{"--yes bash", nil, true,
			"‹shell› show it\n$ " + command + "\n\nline one", "more lines"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCoder(t)
			out := &captureOut{}
			c.Out = out
			c.SuggestShellCommands = true
			c.Confirm = tc.answer
			if tc.grant {
				c.Grants = NewGrants(map[string]bool{GrantBash: true})
				c.Confirm = AutoConfirmer{Granted: c.Grants.Effective}
			}
			if _, ran := c.runShell(context.Background(), toolCommand{command: command, purpose: "show it"}); !ran {
				t.Fatal("the command did not run")
			}
			joined := strings.Join(out.lines, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, joined)
			}
			if strings.Contains(joined, tc.notWant) {
				t.Errorf("output has %q:\n%s", tc.notWant, joined)
			}
		})
	}
}

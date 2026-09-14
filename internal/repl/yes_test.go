package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
)

func yesREPL(t *testing.T) (*REPL, *coder.Coder, *syncBuffer) {
	t.Helper()
	r, cdr, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	cdr.Grants = coder.NewGrants(nil)
	return r, cdr, out
}

// Bare reports. The house pattern, and the reason it matters here: a command
// typed to ask what is auto-approved must not approve anything.
func TestYesBareReportsAndChangesNothing(t *testing.T) {
	r, cdr, out := yesREPL(t)
	r.dispatch(context.Background(), "/yes")
	if len(cdr.Grants.Effective()) != 0 {
		t.Error("bare /yes granted something")
	}
	got := out.String()
	if !strings.Contains(got, "Nothing is auto-approved") {
		t.Errorf("the empty report does not say so:\n%s", got)
	}
}

func TestYesAddAndDrop(t *testing.T) {
	r, cdr, out := yesREPL(t)
	r.dispatch(context.Background(), "/yes add websearch")
	if !cdr.Grants.Granted(coder.GrantWebsearch) {
		t.Fatal("/yes add did not grant")
	}
	if got := out.String(); !strings.Contains(got, "websearch") || !strings.Contains(got, "session") {
		t.Errorf("the report does not name the grant and its source:\n%s", got)
	}
	// The other prompts have to be named too: which ones still stop a turn is
	// the thing being decided.
	if !strings.Contains(out.String(), "Still asked") {
		t.Errorf("the report does not say what still asks:\n%s", out.String())
	}
	r.dispatch(context.Background(), "/yes drop websearch")
	if cdr.Grants.Granted(coder.GrantWebsearch) {
		t.Error("/yes drop did not revoke")
	}
}

// The mid-turn revocation that motivated the command: a grant from --yes has to
// be droppable, and reset has to put it back.
func TestYesDropsAFlagGrantAndResetRestoresIt(t *testing.T) {
	r, cdr, _ := yesREPL(t)
	cdr.Grants = coder.NewGrants(map[string]bool{coder.GrantBash: true})

	r.dispatch(context.Background(), "/yes drop bash")
	if cdr.Grants.Granted(coder.GrantBash) {
		t.Fatal("a --yes grant could not be dropped for the session")
	}
	r.dispatch(context.Background(), "/yes reset")
	if !cdr.Grants.Granted(coder.GrantBash) {
		t.Error("reset did not restore the --yes grant")
	}
}

// An unknown name must name what would have worked, and must not half-apply the
// rest of the line.
func TestYesRejectsUnknownNames(t *testing.T) {
	r, cdr, out := yesREPL(t)
	r.dispatch(context.Background(), "/yes add websearch,webserch")
	if len(cdr.Grants.Effective()) != 0 {
		t.Error("a line with a bad name granted part of itself")
	}
	if got := out.String(); !strings.Contains(got, "webserch") || !strings.Contains(got, "websearch") {
		t.Errorf("the error does not name the typo and the alternatives:\n%s", got)
	}
}

func TestYesRejectsAnUnknownSubcommand(t *testing.T) {
	r, _, out := yesREPL(t)
	r.dispatch(context.Background(), "/yes enable bash")
	if !strings.Contains(out.String(), "Usage: /yes") {
		t.Errorf("want the usage line:\n%s", out.String())
	}
}

// Granting everything removes every stop in the session, so say so rather than
// printing a tidy list and an empty "still asked".
func TestYesWarnsWhenNothingWillStopATurn(t *testing.T) {
	r, _, out := yesREPL(t)
	r.dispatch(context.Background(), "/yes add all")
	if !strings.Contains(out.String(), "nothing will stop a turn") {
		t.Errorf("no warning when everything is granted:\n%s", out.String())
	}
}

// Standing grants are not session state the resume file can restore, and they
// must not make dispatch rewrite it on every /yes.
func TestYesIsNotResumeState(t *testing.T) {
	r, _, _ := replWithSaves(t)
	r.coder.Grants = coder.NewGrants(nil)
	before := r.resumeState()
	r.dispatch(context.Background(), "/yes add websearch")
	if r.resumeState() != before {
		t.Error("/yes changed resume state")
	}
}

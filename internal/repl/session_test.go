package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/history"
)

// fakeSessions records what /session asked the host to do, so the tests below
// are about the command's vocabulary rather than about the state directory.
type fakeSessions struct {
	current  string
	rows     []history.Session
	switched []string // "name create" per call
	forked   []string
	renamed  []string
	deleted  []string
	fail     error
}

func (f *fakeSessions) ops() *SessionOps {
	return &SessionOps{
		Current: func() string { return f.current },
		List:    func() ([]history.Session, error) { return f.rows, f.fail },
		Switch: func(name string, create bool, alias string) (string, error) {
			if f.fail != nil {
				return "", f.fail
			}
			f.switched = append(f.switched, name+" create="+boolWord(create)+" alias="+alias)
			f.current = name
			return "Now in " + name + ".", nil
		},
		Fork: func(name, alias string) (string, error) {
			if f.fail != nil {
				return "", f.fail
			}
			f.forked = append(f.forked, name+" alias="+alias)
			return "Now in " + name + ".", nil
		},
		Rename: func(from, to string) error {
			f.renamed = append(f.renamed, from+" -> "+to)
			return f.fail
		},
		Delete: func(name string) error {
			f.deleted = append(f.deleted, name)
			return f.fail
		},
	}
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// replWithSessions is a REPL whose only interesting option is Sessions. The
// commands here are called directly rather than typed, so there is no input.
func replWithSessions(t *testing.T, f *fakeSessions) (*REPL, *syncBuffer) {
	t.Helper()
	r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader(""))
	if f != nil {
		r.opts.Sessions = f.ops()
	}
	return r, out
}

// The verbs carry the model alias the REPL owns, because /model changes it and
// the host has no other way to know which model a new session is running.
func TestSessionVerbsCarryTheLiveModelAlias(t *testing.T) {
	f := &fakeSessions{current: "default"}
	r, _ := replWithSessions(t, f)
	r.opts.ModelAlias = "big"

	cmdSession(context.Background(), r, "new spike")
	cmdSession(context.Background(), r, "switch default")
	cmdSession(context.Background(), r, "fork impl")

	want := []string{"spike create=true alias=big", "default create=false alias=big"}
	if strings.Join(f.switched, "; ") != strings.Join(want, "; ") {
		t.Errorf("switches = %v, want %v", f.switched, want)
	}
	if len(f.forked) != 1 || f.forked[0] != "impl alias=big" {
		t.Errorf("forks = %v", f.forked)
	}
}

// new and switch are separate words on purpose: opening a conversation that is
// not there and creating one over a conversation that is are different
// mistakes, and the host is told which was meant rather than guessing.
func TestNewAndSwitchAreDistinctIntentions(t *testing.T) {
	f := &fakeSessions{current: "default"}
	r, _ := replWithSessions(t, f)

	cmdSession(context.Background(), r, "new a")
	cmdSession(context.Background(), r, "switch b")

	if len(f.switched) != 2 {
		t.Fatalf("switches = %v", f.switched)
	}
	if !strings.Contains(f.switched[0], "create=true") {
		t.Errorf("`new` did not ask to create: %q", f.switched[0])
	}
	if !strings.Contains(f.switched[1], "create=false") {
		t.Errorf("`switch` asked to create: %q", f.switched[1])
	}
}

// /session rename takes the new name only: in the REPL you are in the session
// being renamed, so naming it twice would be ceremony.
func TestSessionRenameRenamesTheOneInUse(t *testing.T) {
	f := &fakeSessions{current: "spike"}
	r, _ := replWithSessions(t, f)

	cmdSession(context.Background(), r, "rename impl")

	if len(f.renamed) != 1 || f.renamed[0] != "spike -> impl" {
		t.Errorf("renames = %v, want the current session renamed", f.renamed)
	}
}

// A verb with no name is a user error, not a call to the host with an empty
// string — which every one of these operations would have taken badly.
func TestSessionVerbsRefuseAnEmptyName(t *testing.T) {
	f := &fakeSessions{current: "default"}
	r, out := replWithSessions(t, f)

	for _, verb := range []string{"new", "switch", "fork", "rename", "delete"} {
		cmdSession(context.Background(), r, verb)
	}
	if len(f.switched)+len(f.forked)+len(f.renamed)+len(f.deleted) != 0 {
		t.Error("a verb with no name reached the host")
	}
	if n := strings.Count(out.String(), "needs a name"); n != 5 {
		t.Errorf("said `needs a name` %d times, want once per verb:\n%s", n, out.String())
	}
}

// Deleting is the one irreversible thing here, and it needs a terminal to ask
// on. Without one it declines rather than proceeding, which is the opposite of
// what every other confirm in this REPL does on an empty answer.
func TestSessionDeleteDeclinesWithoutATerminal(t *testing.T) {
	f := &fakeSessions{current: "spike", rows: []history.Session{{Name: "old", Turns: 3}}}
	r, out := replWithSessions(t, f)
	r.opts.StdinIsTerminal = func() bool { return false }

	cmdSession(context.Background(), r, "delete old")

	if len(f.deleted) != 0 {
		t.Errorf("deleted %v without asking", f.deleted)
	}
	if !strings.Contains(out.String(), "interactive terminal") {
		t.Errorf("did not say why it declined:\n%s", out.String())
	}
	// And it said what would have been lost before declining.
	if !strings.Contains(out.String(), "3 turns") {
		t.Errorf("did not say what deleting would cost:\n%s", out.String())
	}
}

// With no Sessions wired there is nowhere for another conversation to be, and
// the command says so rather than panicking on a nil.
func TestSessionIsDisabledWithoutState(t *testing.T) {
	r, out := replWithSessions(t, nil)
	cmdSession(context.Background(), r, "new spike")
	if !strings.Contains(out.String(), "Sessions are unavailable") {
		t.Errorf("a REPL with no sessions said:\n%s", out.String())
	}
}

func TestSessionListMarksTheOneInUse(t *testing.T) {
	f := &fakeSessions{current: "spike", rows: []history.Session{
		{Name: "spike", Current: true, Turns: 2},
		{Name: "default", Turns: 0},
	}}
	r, out := replWithSessions(t, f)

	cmdSession(context.Background(), r, "")

	got := out.String()
	if !strings.Contains(got, "* spike — 2 turns") {
		t.Errorf("the session in use is not marked:\n%s", got)
	}
	if !strings.Contains(got, "default — no turns") {
		t.Errorf("an empty session should say so rather than print a zero:\n%s", got)
	}
}

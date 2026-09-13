package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

// What /consult puts in the chat has to survive to the next turn *labelled*.
// That is the whole difference from /model, whose answer arrives as an assistant
// turn the session cannot tell from its own memory, so the check is on the wire
// of the following request rather than on the screen: a label the user reads and
// the model never receives would look identical here and be worthless.
func TestConsultAddsTheAnswerAttributed(t *testing.T) {
	stub := &fixture.StreamStub{Turns: []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "I would use a mutex."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "Ok."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
	}}
	var followUp string
	stub.OnRequest = func(n int, req llm.Request, _ *fixture.Request) error {
		if n != 1 {
			return nil
		}
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Role + ": " + m.Text() + "\n")
		}
		followUp = b.String()
		return nil
	}

	r, _, out := newTestREPL(t, stub,
		strings.NewReader("/consult other how should I lock this?\ny\nwhat do you make of that?\n/exit\n"))
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "Added other's answer to the chat.") {
		t.Errorf("the answer was not added:\n%s", out.String())
	}
	if !strings.Contains(followUp, "I would use a mutex.") {
		t.Fatalf("the answer did not reach the next turn:\n%s", followUp)
	}
	// Attributed, and not as something this session said.
	if !strings.Contains(followUp, "The user asked other") {
		t.Errorf("the answer reached the next turn unattributed:\n%s", followUp)
	}
	for line := range strings.SplitSeq(followUp, "\n") {
		if strings.Contains(line, "I would use a mutex.") && strings.HasPrefix(line, llm.RoleAssistant+":") {
			t.Errorf("the advisor's answer arrived as an assistant turn, which is the /model failure:\n%s", line)
		}
	}
}

// A question typed without an alias lands here, which is the case for naming the
// advisor positionally: the aliases are a closed set, so the mistake has an
// error that can say what was expected.
func TestConsultRefusesAnUnknownAlias(t *testing.T) {
	stub := &fixture.StreamStub{}
	r, _, out := newTestREPL(t, stub, strings.NewReader("/consult how do I lock this?\n/exit\n"))
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `Unknown model alias "how"`) {
		t.Errorf("a question with no alias was not refused by name:\n%s", got)
	}
	if !strings.Contains(got, "aliases: other, test") {
		t.Errorf("the refusal does not say which aliases there are:\n%s", got)
	}
}

// Declining leaves the chat alone. Without this the "added it" case passes for a
// command that adds the answer whatever the user says, which is the shape that
// makes the confirmation theatre.
func TestConsultDeclinedAddsNothing(t *testing.T) {
	stub := &fixture.StreamStub{Turns: []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "I would use a mutex."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
		{Events: []fixture.Event{
			{Kind: "Answer", Text: "Ok."},
			{Kind: "Finish", FinishReason: "stop"},
		}},
	}}
	var followUp string
	stub.OnRequest = func(n int, req llm.Request, _ *fixture.Request) error {
		if n != 1 {
			return nil
		}
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Text() + "\n")
		}
		followUp = b.String()
		return nil
	}

	r, _, out := newTestREPL(t, stub,
		strings.NewReader("/consult other how should I lock this?\nn\nwhat do you make of that?\n/exit\n"))
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Added other's answer to the chat.") {
		t.Errorf("a declined answer was added anyway:\n%s", out.String())
	}
	if strings.Contains(followUp, "I would use a mutex.") {
		t.Errorf("a declined answer still reached the next turn:\n%s", followUp)
	}
}

// The add-output prompt asks every time, and this is the check that it does.
//
// It used to carry a Group, so an "a" recorded an auto-approval — and since
// /consult, /run and /check share the group and none of them is a turn, that
// record lasted until the user's next message and covered all three. Answering
// "a" at a consult silently added the next /run's output to the chat.
//
// The rig is the input stream itself: if the second prompt were skipped, the
// "n" below would not be consumed as an answer and would go to the model as a
// message, leaving the output added. So the assertion discriminates in both
// directions without needing to see the prompt, which readline writes straight
// to the terminal where a scripted session cannot capture it.
func TestAddOutputAsksEveryTime(t *testing.T) {
	turn := func(text string) fixture.Turn {
		return fixture.Turn{Events: []fixture.Event{
			{Kind: "Answer", Text: text}, {Kind: "Finish", FinishReason: "stop"},
		}}
	}
	stub := &fixture.StreamStub{Turns: []fixture.Turn{
		turn("ADVICE-ONE"), turn("ADVICE-TWO"), turn("Ok."),
	}}
	// "a" is answered once at a /consult and once at a /run, because either is a
	// place the record could have been written, and the command that writes it is
	// not the only one it used to reach.
	r, _, out := newTestREPL(t, stub, strings.NewReader(
		"/consult other one\n"+
			"a\n"+ // "all turn", if there were such a thing here
			"/consult other two\n"+
			"n\n"+
			"/run echo hello\n"+
			"a\n"+
			"/run echo hello\n"+
			"n\n"+
			"/exit\n"))
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if n := strings.Count(got, "Added other's answer to the chat."); n != 1 {
		t.Errorf("the answer was added %d times, want 1 (the second consult was declined):\n%s", n, got)
	}
	if n := strings.Count(got, "Added the command output to the chat."); n != 1 {
		t.Errorf("the command output was added %d times, want 1 (the second /run was declined):\n%s", n, got)
	}
}

// `scope` is a subcommand word, not a --scope flag: the question is
// rest-of-line prose, and a flag parser would have to guess whether `--scope`
// inside a question is a flag or part of the question.
func TestConsultScopeShowsAndSets(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	r.opts.ConsultScope = coder.ConsultFiles

	cmdConsult(context.Background(), r, "scope")
	if !strings.Contains(out.String(), "Consult scope: files") {
		t.Errorf("bare scope did not report the current value:\n%s", out.String())
	}
	if r.opts.ConsultScope != coder.ConsultFiles {
		t.Error("reporting the scope changed it")
	}

	cmdConsult(context.Background(), r, "scope chat")
	if r.opts.ConsultScope != coder.ConsultChat {
		t.Errorf("scope = %v, want chat", r.opts.ConsultScope)
	}
}

func TestConsultScopeRejectsAnUnknownName(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	r.opts.ConsultScope = coder.ConsultFiles

	cmdConsult(context.Background(), r, "scope everything")
	if r.opts.ConsultScope != coder.ConsultFiles {
		t.Error("an unknown scope name changed the setting")
	}
	got := out.String()
	if !strings.Contains(got, "Unknown consult scope") {
		t.Errorf("want the refusal:\n%s", got)
	}
	// The closed set, so the reader does not have to go and find it.
	for _, name := range coder.ConsultScopeNames {
		if !strings.Contains(got, name) {
			t.Errorf("the refusal omits the valid scope %q:\n%s", name, got)
		}
	}
}

// The counter-arm: `scope` must not eat a normal consultation. A question is
// still a question, and an advisor alias is still looked up.
func TestConsultStillConsults(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))

	cmdConsult(context.Background(), r, "nosuchalias what about scope")
	if !strings.Contains(out.String(), "Unknown model alias") {
		t.Errorf("a normal consultation was not attempted:\n%s", out.String())
	}
}

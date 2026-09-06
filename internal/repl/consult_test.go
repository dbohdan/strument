package repl

import (
	"context"
	"strings"
	"testing"

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

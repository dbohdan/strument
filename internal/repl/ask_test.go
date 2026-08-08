package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

func answerTurn(text string) fixture.Turn {
	return fixture.Turn{Events: []fixture.Event{
		{Kind: "Answer", Text: text},
		{Kind: "Finish", FinishReason: "stop"},
	}}
}

func TestAskOneShotRevertsAndCarriesContext(t *testing.T) {
	var captured []string
	stub := &fixture.StreamStub{
		Turns: []fixture.Turn{
			answerTurn("This function greets the user."),
			answerTurn("Sure, here is how I would refactor it."),
		},
		OnRequest: func(_ int, req llm.Request, _ *fixture.Request) error {
			var b strings.Builder
			for _, m := range req.Messages {
				b.WriteString(m.Role + ": " + m.Text() + "\n")
			}
			captured = append(captured, b.String())
			return nil
		},
	}

	// /ask <q> is a one-shot: it runs in ask format, then reverts to the
	// model's default (diff). The following bare message runs in code mode
	// with the ask Q&A already in context.
	r, cdr, _ := newTestREPL(t, stub,
		strings.NewReader("/ask what does hello.txt do?\nnow refactor it\n/exit\n"))
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("expected 2 sends, got %d", len(captured))
	}
	// Send 1 used the ask prompt set.
	if !strings.Contains(captured[0], "expert code analyst") {
		t.Errorf("send 1 was not in ask mode:\n%s", captured[0])
	}
	// Send 2 used the code (editblock) prompt set — the format reverted.
	if !strings.Contains(captured[1], "expert software developer") {
		t.Errorf("send 2 was not in code mode:\n%s", captured[1])
	}
	// The ask Q&A is carried into the code-mode request (the whole point).
	if !strings.Contains(captured[1], "what does hello.txt do?") ||
		!strings.Contains(captured[1], "This function greets the user.") {
		t.Errorf("send 2 did not carry the ask Q&A:\n%s", captured[1])
	}
	// The active format is back to the model default after the one-shot.
	if cdr.EditFormat() != "tool" {
		t.Errorf("format did not revert after one-shot /ask: %q", cdr.EditFormat())
	}
}

func TestAskPersistentSwitchAndPrompt(t *testing.T) {
	stub := &fixture.StreamStub{Turns: []fixture.Turn{answerTurn("It greets.")}}
	r, cdr, out := newTestREPL(t, stub,
		strings.NewReader("/ask\nwhat is this?\n/code\n/exit\n"))
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Ask mode:") {
		t.Errorf("no ask-mode announcement:\n%s", got)
	}
	if !strings.Contains(got, "Code mode:") {
		t.Errorf("no code-mode announcement:\n%s", got)
	}
	// After /code, the format is back to the default.
	if cdr.EditFormat() != "tool" {
		t.Errorf("format after /code = %q, want tool", cdr.EditFormat())
	}
}

package repl

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/llm"
)

// interruptedStub streams some text and then reports the interrupt the way a
// cancelled send does: context.Canceled from the event iterator, which
// streamOnce turns into resInterrupted.
type interruptedStub struct{ said string }

func (s interruptedStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: s.said}, nil) {
			return
		}
		yield(llm.StreamEvent{}, context.Canceled)
	}
}

// An interrupted turn says the conversation survived it.
//
// The capability was there long before the line was: the interrupted reply and
// everything before it stay in the chat, so the next message continues from
// that point. What the user saw was "^C again to exit" and a fresh prompt,
// which reads as a kill. This pins the line, because the feature *is* the line
// — without it the behaviour is unchanged and unusable.
func TestInterruptedTurnSaysTheChatSurvived(t *testing.T) {
	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = interruptedStub{said: "Starting on that"}

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		Stdin:      strings.NewReader("do the thing\n/exit\n"),
		Stdout:     out,
		Stderr:     out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Your next message continues from here") {
		t.Errorf("no interrupt hint after an interrupted turn:\n%s", got)
	}
	if cdr.LastOutcome() != coder.OutcomeInterrupted {
		t.Errorf("LastOutcome = %v, want OutcomeInterrupted", cdr.LastOutcome())
	}
}

// ...and a turn that finished normally does not.
//
// The counter-case matters more than it looks. A hint printed after every turn
// is noise, and noise after the ninety-nine ordinary turns is what stops it
// being read on the hundredth.
func TestFinishedTurnSaysNothingAboutInterrupts(t *testing.T) {
	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = plainAnswerStub("Done.")

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		Stdin:      strings.NewReader("do the thing\n/exit\n"),
		Stdout:     out,
		Stderr:     out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); strings.Contains(got, "continues from here") {
		t.Errorf("interrupt hint after a turn that was not interrupted:\n%s", got)
	}
}

type plainAnswerStub string

func (s plainAnswerStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: string(s)}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

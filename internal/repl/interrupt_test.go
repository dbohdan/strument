package repl

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

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

// Two Ctrl-C still exits, even when the second one lands on the interrupt
// question.
//
// The chord lives in withinTurn's signal handler, which only sees SIGINT.
// While a question is up readline holds the terminal in raw mode, ISIG is off,
// and Ctrl-C arrives as a byte that readline turns into ErrInterrupt — so the
// signal handler never runs. A live pty probe caught it: two Ctrl-C 50ms apart
// during a turn produced one "^C again to exit" and no exit, silently
// weakening a promise users hold.
//
// Now stubbed rather than real, so the window is a fact of the test rather
// than a race with it.
func TestChordExitsFromTheInterruptQuestion(t *testing.T) {
	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = interruptedStub{said: "Starting on that"}

	var exited []int
	now := time.Unix(1_700_000_000, 0)
	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		// The question is read from stdin, so the first byte it sees is the
		// second Ctrl-C of the chord.
		Stdin:           strings.NewReader("do the thing\n\x03"),
		Stdout:          out,
		Stderr:          out,
		IsTerminal:      func() bool { return false },
		StdinIsTerminal: func() bool { return true },
		Now:             func() time.Time { return now },
		Exit:            func(code int) { exited = append(exited, code) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cdr.Asker = r.Asker()

	// The interrupt that cancelled the send is the chord's first press.
	r.chord()

	_ = r.Run(context.Background())

	if len(exited) == 0 {
		t.Fatalf("the chord did not exit:\n%s", out.String())
	}
	if exited[0] != 130 {
		t.Errorf("exit code = %d, want 130", exited[0])
	}
}

// ...and a lone Ctrl-C long after the first does not exit.
//
// The chord is a chord. A second press outside the window means "stop", which
// is what cancelling the question already does, and exiting there would make
// Strument quit on a keystroke the user meant for the prompt in front of them.
func TestLateCtrlCAtTheQuestionDoesNotExit(t *testing.T) {
	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = interruptedStub{said: "Starting on that"}

	var exited []int
	clock := time.Unix(1_700_000_000, 0)
	out := &syncBuffer{}
	r, err := New(Options{
		Coder:           cdr,
		Config:          testConfig(model),
		ModelAlias:      "test",
		Stdin:           strings.NewReader("do the thing\n\x03"),
		Stdout:          out,
		Stderr:          out,
		IsTerminal:      func() bool { return false },
		StdinIsTerminal: func() bool { return true },
		Now:             func() time.Time { return clock },
		Exit:            func(code int) { exited = append(exited, code) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cdr.Asker = r.Asker()

	r.chord()
	clock = clock.Add(10 * time.Second) // well outside the window

	_ = r.Run(context.Background())

	if len(exited) != 0 {
		t.Errorf("exited %v on a lone Ctrl-C outside the chord window", exited)
	}
}

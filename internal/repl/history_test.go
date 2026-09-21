package repl

import (
	"context"
	"strings"
	"sync"
	"testing"

	"dbohdan.com/strument/internal/coder"
)

// records collects the session record the REPL's turn produces.
//
// Held under a mutex because the REPL runs the turn on its own goroutine and
// the test reads the slice after Run returns; -race objects otherwise.
type records struct {
	mu   sync.Mutex
	recs []coder.Record
}

func (r *records) Record(rec coder.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
}

func (r *records) turn() (coder.Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.recs {
		if rec.Type == "turn" {
			return rec, true
		}
	}
	return coder.Record{}, false
}

// A turn run from the REPL reaches the session record.
//
// This used to check a markdown transcript the REPL appended to itself. The
// record is written by the coder now, which is why the REPL has nothing to do
// with it any more — and is the reason this test is worth keeping: the wiring
// moved, and a turn that left no record would look exactly like a working
// session until someone went looking for it.
func TestREPLTurnsReachTheSessionRecord(t *testing.T) {
	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = answerStub("Here is the **answer**.\n")
	rec := &records{}
	cdr.Recorder = rec

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
	cdr.Confirm = coder.AutoConfirmer{Fallback: r.Confirmer()}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	turn, ok := rec.turn()
	if !ok {
		t.Fatal("the REPL ran a turn and left no turn record")
	}
	if turn.Prompt != "do the thing" {
		t.Errorf("prompt = %q, want what was typed", turn.Prompt)
	}
	if !strings.Contains(turn.Answer, "Here is the **answer**.") {
		t.Errorf("answer = %q, want the model's reply", turn.Answer)
	}
	if !strings.Contains(turn.Model, "test-model") {
		t.Errorf("model = %q, want the qualified slug", turn.Model)
	}
	if turn.Time == "" {
		t.Error("the turn record carries no time")
	}
}

package coder

import (
	"context"
	"iter"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// untrackedRepo answers UntrackedFiles from a script: the first call is the
// turn's starting snapshot, every later one the state after its commands ran.
type untrackedRepo struct {
	fakeRepo

	root   string
	before []string
	after  []string
	calls  int
}

func (r *untrackedRepo) Root() string { return r.root }

func (r *untrackedRepo) UntrackedFiles() ([]string, error) {
	r.calls++
	if r.calls == 1 {
		return r.before, nil
	}
	return r.after, nil
}

// writeThenAnswer writes made.txt through the edit tools, then answers every
// later send, keeping each request so the test can read what it was sent.
type writeThenAnswer struct {
	reqs []llm.Request
}

func (s *writeThenAnswer) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.reqs = append(s.reqs, req)
	first := len(s.reqs) == 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if first {
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, ID: "call_1", Name: "write", Args: `{"path":"made.txt","content":"x\n"}`,
			}}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// The list is what the turn's commands made and nothing else: not a file
// that was untracked before the turn, and not one the edit tools wrote. The
// model hears about it once, when it believes it is done, and the turn record
// keeps what was still there at the end.
func TestNewFilesAreNotedOnce(t *testing.T) {
	c := testCoder(t)
	repo := &untrackedRepo{
		root:   c.Root,
		before: []string{"old-scratch.txt"},
		after:  []string{"old-scratch.txt", "made.txt", "bin/cmain"},
	}
	c.Repo = repo
	client := &writeThenAnswer{}
	c.Client = client
	rec := &capture{}
	c.Recorder = rec

	c.runOne(context.Background(), "do the thing")

	// write, answer, answer to the note — and no fourth send: once per turn.
	if len(client.reqs) != 3 {
		t.Fatalf("%d sends, want 3 (write, answer, reply to the note)", len(client.reqs))
	}
	msgs := client.reqs[2].Messages
	note := msgs[len(msgs)-1].Text()
	if !strings.Contains(note, "- bin/cmain") {
		t.Errorf("the note does not list bin/cmain:\n%s", note)
	}
	for _, not := range []string{"made.txt", "old-scratch.txt"} {
		if strings.Contains(note, not) {
			t.Errorf("the note lists %s, which the turn's commands did not create:\n%s", not, note)
		}
	}
	if !strings.Contains(note, "may be meant to stay") {
		t.Errorf("the note should say that leaving the files is a fine answer:\n%s", note)
	}

	var turn *Record
	for i := range rec.recs {
		if rec.recs[i].Type == "turn" {
			turn = &rec.recs[i]
		}
	}
	if turn == nil || !slices.Equal(turn.CreatedFiles, []string{"bin/cmain"}) {
		t.Errorf("turn record created_files = %v, want [bin/cmain]", turn)
	}
}

// Without a repository there is no snapshot and no note, and the turn ends
// when the model says it is done.
func TestNoNewFilesNoteWithoutARepo(t *testing.T) {
	c := testCoder(t)
	client := &writeThenAnswer{}
	c.Client = client

	c.runOne(context.Background(), "do the thing")

	if len(client.reqs) != 2 {
		t.Errorf("%d sends, want 2: nothing to note without git", len(client.reqs))
	}
}

package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
)

// TestClearAndResetStayInTheSession: both commands are soft restarts. The
// conversation goes and the session does not — /clear keeps the pins, /reset
// drops them after, notes survive both, and since neither writes to the
// record, both say that --continue restores what was cleared.
func TestClearAndResetStayInTheSession(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = answerStub("Reply.\n")
	cdr.Session = "foo"
	cdr.SessionNotes, cdr.SessionNotesSession = "Keep the API stable.", "earlier"

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		SaveResume: func(string) {},
		Stdin: strings.NewReader(
			"/add hello.txt\n" +
				"hi there\n" +
				"/clear\n" +
				"/ls\n" +
				"/reset\n" +
				"/exit\n"),
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
	got := out.String()

	// The stub turn ran, so there was a conversation to clear.
	if !strings.Contains(got, "Reply.") {
		t.Fatalf("the stub turn did not run; the test proves nothing:\n%s", got)
	}
	if fold := cdr.ViewContext(-1); strings.Contains(fold, "Reply.") {
		t.Errorf("the conversation survived /clear:\n%s", fold)
	}

	const (
		cleared = "Chat history cleared. The record keeps the conversation; --continue restores it."
		reset   = "Unpinned everything and cleared the chat history. The record keeps the conversation; --continue restores it."
	)
	// /ls runs between the two clears: the pin it lists is the one /clear kept.
	iClear := strings.Index(got, cleared)
	iReset := strings.Index(got, reset)
	if iClear == -1 || iReset == -1 {
		t.Fatalf("a clear message is missing:\n%s", got)
	}
	iLS := strings.Index(got[iClear:iReset], "Pinned:")
	if iLS == -1 {
		t.Errorf("/ls after /clear listed no pins:\n%s", got)
	} else if !strings.Contains(got[iClear:iReset], "hello.txt") {
		t.Errorf("/clear dropped the pins:\n%s", got)
	}

	if cdr.Session != "foo" {
		t.Errorf("session = %q; /clear and /reset must not switch sessions", cdr.Session)
	}
	if len(cdr.ChatFiles()) != 0 {
		t.Errorf("/reset kept the pins: %v", cdr.ChatFiles())
	}
	if cdr.SessionNotes != "Keep the API stable." || cdr.SessionNotesSession != "earlier" {
		t.Errorf("notes = %q from %q; want them kept — neither command drops notes",
			cdr.SessionNotes, cdr.SessionNotesSession)
	}
}

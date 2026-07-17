package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/history"
)

func TestREPLWritesChatHistory(t *testing.T) {
	root := t.TempDir()
	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = answerStub("Here is the **answer**.\n")

	histPath := filepath.Join(t.TempDir(), "chat.md")
	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		History:    history.New(histPath),
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

	data, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("history file not written: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"# Strument chat history",
		"### Prompt\n\ndo the thing",
		"### Response\n\nHere is the **answer**.",
		"test-model", // the model slug in the header
	} {
		if !strings.Contains(got, want) {
			t.Errorf("history missing %q:\n%s", want, got)
		}
	}
}

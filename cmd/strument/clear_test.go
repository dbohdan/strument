package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/history"
)

// TestClearMovesToTheNextSession drives the switcher behind /clear and /reset.
// A session with turns is left whole under its name and the process moves to
// the next in the sequence, carrying the notes as they are and, for /clear,
// the pins; a session with nothing recorded is emptied in place.
func TestClearMovesToTheNextSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := history.EnsureProjectDir(root, ""); err != nil {
		t.Fatal(err)
	}
	model := &config.Model{Provider: config.Provider{Adapter: config.AdapterOpenRouter}, Slug: "m", EditFormat: "tool"}
	model.SideModel = model
	cdr := coder.New(root, model)
	cdr.Session = "foo"
	sw := &sessionSwitcher{cdr: cdr, projectRoot: root, saveResume: func(string) {}}

	recordTurn := func(session string) {
		t.Helper()
		dir, err := history.SessionDir(root, session)
		if err != nil {
			t.Fatal(err)
		}
		log := filepath.Join(dir, "log")
		if err := os.MkdirAll(log, 0o755); err != nil {
			t.Fatal(err)
		}
		row := `{"type":"turn","answer":"done"}` + "\n"
		if err := os.WriteFile(filepath.Join(log, "20260926T000000.000Z.jsonl"), []byte(row), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := history.EnsureSessionDir(root, "foo"); err != nil {
		t.Fatal(err)
	}
	cdr.AddFile("main.go")
	cdr.SessionNotes, cdr.SessionNotesSession = "Keep the API stable.", "earlier"

	// Nothing recorded: emptied in place, no new name.
	if line, err := sw.clear("m", true); err != nil || line != "" || cdr.Session != "foo" {
		t.Fatalf("empty session: line=%q err=%v session=%q; want an in-place clear", line, err, cdr.Session)
	}

	recordTurn("foo")
	line, err := sw.clear("m", true)
	if err != nil {
		t.Fatal(err)
	}
	if cdr.Session != "foo-2" || !strings.Contains(line, "foo keeps the earlier conversation") {
		t.Fatalf("session %q, line %q; want foo-2 and foo named as keeping the conversation", cdr.Session, line)
	}
	if !slices.Contains(cdr.ChatFiles(), "main.go") {
		t.Errorf("/clear dropped the pins: %v", cdr.ChatFiles())
	}
	if cdr.SessionNotes != "Keep the API stable." || cdr.SessionNotesSession != "earlier" {
		t.Errorf("notes = %q from %q; want them carried unchanged", cdr.SessionNotes, cdr.SessionNotesSession)
	}
	if turns := history.SessionTurns(root, "foo"); turns != 1 {
		t.Errorf("foo has %d turns after the clear; its record must be left whole", turns)
	}

	recordTurn("foo-2")
	if _, err := sw.clear("m", false); err != nil {
		t.Fatal(err)
	}
	if cdr.Session != "foo-3" {
		t.Errorf("second clear went to %q, want foo-3", cdr.Session)
	}
	if len(cdr.ChatFiles()) != 0 {
		t.Errorf("/reset kept the pins: %v", cdr.ChatFiles())
	}
	if cdr.SessionNotes == "" {
		t.Error("/reset dropped the notes; it never did before")
	}
}

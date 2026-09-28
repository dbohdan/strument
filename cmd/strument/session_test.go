package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/history"
)

// TestSessionLogRecording: Recording follows the segment, not the struct. The
// log is wired for the whole process, so a caller that asked "is there a log?"
// would be told yes after a close, and /clear would promise a restore from a
// record that stopped being written.
func TestSessionLogRecording(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := history.EnsureProjectDir(root, ""); err != nil {
		t.Fatal(err)
	}
	l := &sessionLog{projectRoot: root}
	if l.Recording() {
		t.Error("a log with no segment reports recording")
	}
	if err := l.Open("default"); err != nil {
		t.Fatal(err)
	}
	if !l.Recording() {
		t.Error("an open segment does not report recording")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if l.Recording() {
		t.Error("a closed log reports recording")
	}
}

// TestRefusedSwitchKeepsRecording: a switch whose record cannot be opened is
// refused, and the run carries on in its session with its segment still open.
// Open used to close the old segment before trying the new one, so a refused
// switch left the rest of the run unrecorded, with nothing on screen to say so.
func TestRefusedSwitchKeepsRecording(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := history.EnsureProjectDir(root, ""); err != nil {
		t.Fatal(err)
	}
	model := &config.Model{Provider: config.Provider{Adapter: config.AdapterOpenRouter}, Slug: "m", EditFormat: "tool"}
	model.SideModel = model
	cdr := coder.New(root, model)
	cdr.Session = "here"
	l := &sessionLog{projectRoot: root}
	if err := l.Open("here"); err != nil {
		t.Fatal(err)
	}
	cdr.Recorder = l
	sw := &sessionSwitcher{cdr: cdr, projectRoot: root, log: l, saveResume: func(string) {}}

	// "there" exists, but a file sits where its log directory belongs, so no
	// segment can be made in it.
	dir, err := history.EnsureSessionDir(root, "there")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = sw.switchTo("there", false, "m")
	if err == nil || !strings.Contains(err.Error(), "still in here") {
		t.Fatalf("switch error = %v; want a refusal that names the session kept", err)
	}
	if cdr.Session != "here" {
		t.Errorf("session = %q after a refused switch; want here", cdr.Session)
	}
	if !cdr.Recording() {
		t.Fatal("a refused switch left the run unrecorded")
	}

	// And what is recorded now lands in the session the run is still in.
	cdr.RecordSession("m")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	segs, err := history.LogSegments(root, "here")
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments of here = %v, %v; want one", segs, err)
	}
	data, err := os.ReadFile(segs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"session"`) {
		t.Errorf("the record written after the refused switch is not in here's segment:\n%s", data)
	}
}

package main

import (
	"testing"

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

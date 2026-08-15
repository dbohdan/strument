package history

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func undoRoot(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	return t.TempDir()
}

func turn(path string, before, after string, existed bool) UndoTurn {
	return UndoTurn{Entries: []UndoEntry{{
		Path: path, Before: []byte(before), After: []byte(after),
		Existed: existed, Mode: 0o644,
	}}}
}

// The point of the whole file: a stack written in one session is readable in the
// next, bytes intact. Contents go through base64, so a file that is not text
// must survive too — the harness is meant to be usable on a live configuration
// directory, where a binary in the tree is ordinary.
func TestUndoRoundTrip(t *testing.T) {
	root := undoRoot(t)
	binary := string([]byte{0x00, 0xff, 0x1b, '\n', 0x7f})

	want := UndoState{
		Turns: []UndoTurn{
			turn("a.txt", "one\n", "two\n", true),
			turn("bin.dat", binary, binary+"!", true),
			turn("new.go", "", "package x\n", false),
		},
		Commits: []string{"abc1234", "def5678"},
		Last:    "def5678",
	}
	if err := SaveUndo(root, want); err != nil {
		t.Fatal(err)
	}

	got := LoadUndo(root)
	if len(got.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(got.Turns))
	}
	if s := string(got.Turns[1].Entries[0].Before); s != binary {
		t.Errorf("binary before-state did not survive: %q", s)
	}
	if got.Turns[2].Entries[0].Existed {
		t.Error("a created file must come back with Existed false, or undo would rewrite instead of remove")
	}
	if got.Turns[0].Entries[0].Mode != 0o644 {
		t.Errorf("mode = %v, want 0644", got.Turns[0].Entries[0].Mode)
	}
	if strings.Join(got.Commits, ",") != "abc1234,def5678" || got.Last != "def5678" {
		t.Errorf("commits = %v, last = %q", got.Commits, got.Last)
	}
}

// The record holds verbatim copies of source, and internal/coder goes to some
// trouble to preserve a file's mode through an edit. World-readable copies one
// directory over would undo that work.
func TestUndoFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission bits")
	}
	root := undoRoot(t)
	if err := SaveUndo(root, UndoState{Turns: []UndoTurn{turn("a", "x", "y", true)}}); err != nil {
		t.Fatal(err)
	}
	p, err := UndoPath(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600 (rename replaces the inode, so it must be created 0600)", perm)
	}
	if dir, err := os.Stat(filepath.Dir(p)); err == nil {
		if perm := dir.Mode().Perm(); perm != 0o700 {
			t.Errorf("project dir mode = %v, want 0700", perm)
		}
	}
}

// A missing, malformed, or future-version file costs nothing more than a lost
// undo — the same judgement LoadResume makes, and safe for a stronger reason
// here: UndoLastTurn refuses any file that no longer matches, so a wrong stack
// can only produce a refusal.
func TestLoadUndoToleratesJunk(t *testing.T) {
	root := undoRoot(t)
	if got := LoadUndo(root); len(got.Turns) != 0 {
		t.Error("a missing file should load as the zero value")
	}

	p, err := UndoPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		t.Fatal(err)
	}
	for _, junk := range []string{
		"not json at all",
		`{"version":999,"turns":[{"entries":[{"path":"a"}]}]}`,
		`{"version":1,`,
	} {
		if err := os.WriteFile(p, []byte(junk), fileMode); err != nil {
			t.Fatal(err)
		}
		if got := LoadUndo(root); len(got.Turns) != 0 {
			t.Errorf("junk %q loaded %d turns, want 0", junk, len(got.Turns))
		}
	}
}

// Only the top of the stack is reachable, so the bottom is what gets evicted.
// The byte cap is the one that matters: entries hold contents twice, before and
// after, which is how a state directory quietly becomes a gigabyte.
func TestUndoRetentionEvictsOldestFirst(t *testing.T) {
	root := undoRoot(t)

	var st UndoState
	for i := range maxUndoTurns + 5 {
		st.Turns = append(st.Turns, turn("f.txt", "old", string(rune('a'+i%26)), true))
	}
	if err := SaveUndo(root, st); err != nil {
		t.Fatal(err)
	}
	got := LoadUndo(root)
	if len(got.Turns) != maxUndoTurns {
		t.Errorf("turns = %d, want the cap %d", len(got.Turns), maxUndoTurns)
	}
	// The newest survived: it is the one /undo would reach first.
	last := got.Turns[len(got.Turns)-1].Entries[0].After
	if string(last) != string(st.Turns[len(st.Turns)-1].Entries[0].After) {
		t.Errorf("the newest turn was evicted; after = %q", last)
	}

	// Byte cap: three turns of 5 MiB each must not all be kept.
	big := strings.Repeat("x", 5<<20)
	heavy := UndoState{Turns: []UndoTurn{
		turn("a", big, big, true),
		turn("b", big, big, true),
		turn("c", big, big, true),
	}}
	if err := SaveUndo(root, heavy); err != nil {
		t.Fatal(err)
	}
	if n := len(LoadUndo(root).Turns); n != 1 {
		t.Errorf("turns kept = %d; 3 x 10 MiB against an %d-byte cap should leave 1", n, maxUndoBytes)
	}
}

// One turn over the cap on its own is still the turn the user is about to undo.
// Refusing to record it would trade a bounded disk cost for unbounded surprise.
func TestUndoAlwaysKeepsTheNewestTurn(t *testing.T) {
	root := undoRoot(t)
	huge := strings.Repeat("y", (maxUndoBytes*2)+1)
	if err := SaveUndo(root, UndoState{Turns: []UndoTurn{turn("big.bin", huge, huge, true)}}); err != nil {
		t.Fatal(err)
	}
	if n := len(LoadUndo(root).Turns); n != 1 {
		t.Errorf("turns = %d, want 1 even though it exceeds the cap alone", n)
	}
}

package history

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestDefaultPathKeying(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	p1, err := DefaultPath("/home/user/myproj")
	if err != nil {
		t.Fatal(err)
	}
	p1again, _ := DefaultPath("/home/user/myproj")
	p2, _ := DefaultPath("/home/user/other")

	if p1 != p1again {
		t.Errorf("same root gave different paths: %q vs %q", p1, p1again)
	}
	if p1 == p2 {
		t.Errorf("different roots gave the same path: %q", p1)
	}
	// The key is the directory now, and it keeps the readable prefix: a
	// listing of projects/ should be legible without resolving hashes.
	if dir := filepath.Base(filepath.Dir(p1)); !strings.HasPrefix(dir, "myproj-") {
		t.Errorf("project dir = %q, want myproj-<hash>", dir)
	}
	if !strings.Contains(p1, filepath.Join("strument", "projects")) {
		t.Errorf("path not under strument/projects: %q", p1)
	}
}

// Input history is per project now, and shares the transcript's key so the two
// files sit adjacent. It was global; real use said a shared file fills with
// prompts that mean nothing in the project you are actually in.
// A project's files live in one directory, and the directory is the unit:
// "forget this project" is an rm -rf, and the mode has one place to be right.
func TestProjectDirLayout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	dir, err := ProjectDir("/tmp/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(dir)) != "projects" {
		t.Errorf("project dir not under projects/: %q", dir)
	}
	transcript, err := DefaultPath("/tmp/alpha")
	if err != nil {
		t.Fatal(err)
	}
	input, err := InputHistoryPath("/tmp/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Join(dir, "transcript.md"); transcript != got {
		t.Errorf("transcript = %q, want %q", transcript, got)
	}
	if got := filepath.Join(dir, "input.txt"); input != got {
		t.Errorf("input history = %q, want %q", input, got)
	}

	other, err := ProjectDir("/tmp/beta")
	if err != nil {
		t.Fatal(err)
	}
	if other == dir {
		t.Errorf("two projects share one directory: %q", dir)
	}
}

// The root file is why the directory is worth having a name that is only a
// hash: it says which project this is without recomputing the hash over
// candidate paths.
func TestEnsureProjectDirWritesRoot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	dir, err := EnsureProjectDir(project)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "root"))
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(project)
	if strings.TrimSpace(string(got)) != abs {
		t.Errorf("root file = %q, want %q", got, abs)
	}
	// Rewritten each session, so calling twice must not append or fail.
	if _, err := EnsureProjectDir(project); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(filepath.Join(dir, "root"))
	if string(got2) != string(got) {
		t.Errorf("root file changed on the second call: %q then %q", got, got2)
	}
}

// A project's state is owner-only. The transcript carries whatever the model
// read out of the project, and Strument is meant to be usable on a live
// configuration directory, where that is an .env as often as it is source.
func TestProjectStateIsOwnerOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	dir, err := EnsureProjectDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission bits")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != dirMode {
		t.Errorf("project dir mode = %04o, want %04o", perm, dirMode)
	}

	p, err := DefaultPath(project)
	if err != nil {
		t.Fatal(err)
	}
	w := New(p)
	if err := w.Append(Turn{User: "q", Assistant: "a"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"transcript.md", "root"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != fileMode {
			t.Errorf("%s mode = %04o, want %04o", name, perm, fileMode)
		}
	}
}

func TestAppendFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "chat.md")
	w := New(path)

	when := time.Date(2026, 7, 17, 14, 30, 5, 0, time.UTC)
	if err := w.Append(Turn{
		Time:           when,
		Model:          "flash",
		TokensSent:     2600,
		TokensReceived: 433,
		Cost:           0.00046,
		CostKnown:      true,
		User:           "change the greeting",
		Assistant:      "Sure, here is the change.",
	}); err != nil {
		t.Fatal(err)
	}
	// Second turn, cost unknown.
	if err := w.Append(Turn{
		Time:           when.Add(time.Minute),
		Model:          "flash",
		TokensSent:     10,
		TokensReceived: 5,
		User:           "thanks",
		Assistant:      "You're welcome.",
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	for _, want := range []string{
		"# Strument chat history",
		"## 2026-07-17 14:30:05 — flash",
		"2600 tokens sent, 433 received · $0.0005",
		"### Prompt\n\nchange the greeting",
		"### Response\n\nSure, here is the change.",
		"## 2026-07-17 14:31:05 — flash",
		"_10 tokens sent, 5 received_", // no cost suffix when unknown
		"You're welcome.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("history missing %q:\n%s", want, got)
		}
	}
	// The title header appears exactly once across appends.
	if n := strings.Count(got, "# Strument chat history"); n != 1 {
		t.Errorf("title header appears %d times, want 1", n)
	}
}

func TestAppendSkipsEmptyAssistant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.md")
	w := New(path)
	if err := w.Append(Turn{User: "hi", Assistant: "   \n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty-answer turn should not create the file: err=%v", err)
	}
}

// The transcript records the talk; without git it is also the only durable
// record of the work, since there are no commits and the undo spill is not
// something a human reads. The assistant's own prose routinely says "done"
// without naming a path.
func TestTranscriptRecordsChangedFiles(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "transcript.md")
	w := New(p)

	if err := w.Append(Turn{
		User: "rename it", Assistant: "Done.",
		Files: []string{"internal/poll/poll.go", "internal/poll/watch.go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Turn{User: "what is this?", Assistant: "A poll loop."}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	for _, want := range []string{"2 files changed", "### Changed", "`internal/poll/poll.go`", "`internal/poll/watch.go`"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
	// A turn that changed nothing says nothing — no empty heading, no "0 files".
	if strings.Contains(got, "0 files changed") || strings.Count(got, "### Changed") != 1 {
		t.Errorf("a no-edit turn should add no Changed section:\n%s", got)
	}
	// One file is singular. plural() elsewhere has been wrong about this before.
	if err := w.Append(Turn{User: "x", Assistant: "y", Files: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(p)
	if !strings.Contains(string(body), "1 file changed") {
		t.Errorf("one file should be singular:\n%s", body)
	}
}

// Notes are a convenience, so every path is forgiving: absent reads as "",
// empty writes as absent, and the file is owner-only like the rest of a
// project's state.
func TestNotesRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	if got := LoadNotes(project); got != "" {
		t.Errorf("no notes should read as empty, got %q", got)
	}
	if err := SaveNotes(project, "Renamed the constant; 45 not 30 because the LB idles at 60."); err != nil {
		t.Fatal(err)
	}
	if got := LoadNotes(project); !strings.Contains(got, "idles at 60") {
		t.Errorf("notes = %q", got)
	}

	// "No notes" has one representation on disk, so a later read cannot find a
	// blank file and treat it as content.
	if err := SaveNotes(project, "   \n"); err != nil {
		t.Fatal(err)
	}
	p, _ := NotesPath(project)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("empty notes should remove the file rather than blank it")
	}
	if got := LoadNotes(project); got != "" {
		t.Errorf("after clearing, notes = %q", got)
	}

	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission bits")
	}
	if err := SaveNotes(project, "x"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Errorf("notes.md mode = %04o, want %04o", perm, fileMode)
	}
}

// The lock file sits beside the transcript so two harness copies keyed to the
// same root cannot write its state at once. Verified here the way the harness
// takes it: two distinct open file descriptions over the same path, the second
// TryLock reporting (false, nil) rather than an error.
func TestProjectLock(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	// The harness creates the project directory (EnsureProjectDir) before taking
	// the lock, so the lock file's parent always exists. Mirror that here.
	if _, err := EnsureProjectDir(project); err != nil {
		t.Fatal(err)
	}
	p, err := LockPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "lock" {
		t.Errorf("lock path = %q, want a file named lock", p)
	}

	first := flock.New(p)
	if ok, err := first.TryLock(); err != nil || !ok {
		t.Fatalf("first TryLock = (%v, %v), want (true, nil)", ok, err)
	}
	defer first.Close()

	second := flock.New(p)
	ok, err := second.TryLock()
	if err != nil {
		t.Fatalf("second TryLock error = %v, want nil with ok=false", err)
	}
	if ok {
		second.Close()
		t.Fatal("second TryLock = (true, nil), want (false, nil): lock did not exclude a concurrent holder")
	}
}

func TestProjectLockReleasedOnClose(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	if _, err := EnsureProjectDir(project); err != nil {
		t.Fatal(err)
	}
	p, err := LockPath(project)
	if err != nil {
		t.Fatal(err)
	}

	first := flock.New(p)
	if ok, err := first.TryLock(); err != nil || !ok {
		t.Fatalf("first TryLock = (%v, %v), want (true, nil)", ok, err)
	}
	// Releasing by closing must clear the lock so a fresh holder can take it.
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := flock.New(p)
	if ok, err := second.TryLock(); err != nil || !ok {
		t.Fatalf("after close, second TryLock = (%v, %v), want (true, nil)", ok, err)
	}
	second.Close()
}

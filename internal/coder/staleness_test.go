package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// touchLater rewrites a file with content that both differs in size and lands
// on a later modification time, so the change is visible to a stamp whatever
// the filesystem's timestamp granularity is.
func touchLater(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// editCall is a search-and-replace edit against the fixture file these
// tests all use.
func editCall(callID, search, replace string) plannedEdit {
	return plannedEdit{callID: callID, path: "a.txt", search: search, replace: replace}
}

// A file the model never read has no stamp, so the gate cannot fire: nothing
// that worked before this change stops working. This is the fail-open half and
// it is checked first, because a gate that refuses edits it knows nothing about
// would be worse than the hole it closes.
func TestUnreadFileIsStillEditable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits(
		[]plannedEdit{editCall("call_1", "world", "mars")},
		results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v, want the edit to be applied", edited)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello mars\n" {
		t.Errorf("file = %q, want the edit applied", got)
	}
}

// The hole this closes. The model reads a file, the user saves a different
// version from their own editor, and the model's edit still matches text that
// has moved. Before this, the write landed silently on content the model never
// saw.
func TestEditIsRefusedAfterTheFileMovedUnderneath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")
	c.shown.note("a.txt", path)

	// Somebody else writes the file. "world" is still in it, so the edit would
	// have applied cleanly and told the model it succeeded.
	touchLater(t, path, "a totally different hello world, rewritten\n")

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits(
		[]plannedEdit{editCall("call_1", "world", "mars")},
		results, &matchFailure)

	if len(edited) != 0 {
		t.Errorf("edited = %v, want the edit refused", edited)
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "mars") {
		t.Error("the edit was written to a file that had moved under the model")
	}
	if !matchFailure {
		t.Error("a stale edit must reflect: the model should read again and retry")
	}
	// The message must name the cause. "Not found" would send the model
	// hunting for a typo it did not make — and here the text *was* found.
	got := results["call_1"]
	for _, want := range []string{"changed on disk", "Read it again"} {
		if !strings.Contains(got, want) {
			t.Errorf("result = %q, want it to mention %q", got, want)
		}
	}
	if strings.Contains(got, "not found") {
		t.Errorf("result = %q, must not report this as a failed match", got)
	}
}

// Strument's own writes must not look like somebody else's. Two edits to one
// file in a turn is ordinary, and the second must not be refused because the
// first changed the file.
func TestOurOwnWriteRefreshesTheStamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one two three\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")
	c.shown.note("a.txt", path)

	results := map[string]string{}
	matchFailure := false
	if edited := c.applyToolEdits(
		[]plannedEdit{editCall("call_1", "one", "ONE")},
		results, &matchFailure); len(edited) != 1 {
		t.Fatalf("first edit: edited = %v, want it applied", edited)
	}

	results = map[string]string{}
	matchFailure = false
	edited := c.applyToolEdits(
		[]plannedEdit{editCall("call_2", "three", "THREE")},
		results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("second edit: edited = %v, want it applied — the harness's own "+
			"write must not read as a change by somebody else: %q", edited, results["call_2"])
	}
	if got, _ := os.ReadFile(path); string(got) != "ONE two THREE\n" {
		t.Errorf("file = %q, want both edits applied", got)
	}
}

// write puts down a whole file and claims nothing about what was there, so it
// is exempt: refusing it would block the one call that can recover a file
// whose state nobody agrees on.
func TestWriteIsNotGatedOnStaleness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")
	c.shown.note("a.txt", path)
	touchLater(t, path, "changed by somebody else entirely\n")

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits(
		[]plannedEdit{wholeFileWrite("call_1", "a.txt", "written fresh\n")},
		results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v, want write to go through: %q", edited, results["call_1"])
	}
	if got, _ := os.ReadFile(path); string(got) != "written fresh\n" {
		t.Errorf("file = %q, want the write applied", got)
	}
}

// Undo rewrites files behind the harness's back. Every stamp then describes a
// version that no longer exists, so they are dropped rather than left to report
// the user's own undo as a change by somebody else.
func TestUndoDropsTheStamps(t *testing.T) {
	s := newShownFiles()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.note("a.txt", path)
	touchLater(t, path, "yy\n")
	if !s.changed("a.txt", path) {
		t.Fatal("the stamp did not notice a change, so this test proves nothing")
	}
	s.forget()
	if s.changed("a.txt", path) {
		t.Error("stamps survived forget()")
	}
}

// TestFuzzyEditIsCountedAndAnnounced is the positive control for M9. A count
// that reads zero in a trial means nothing unless the counter can read one, so
// this drives the whole applied-edit path with a search whose indentation does
// not occur in the file: only the line matcher can place it.
func TestFuzzyEditIsCountedAndAnnounced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	// Tab-indented, as Go is.
	if err := os.WriteFile(path, []byte("func main() {\n\tprintln(\"hi\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")

	results := map[string]string{}
	matchFailure := false
	// Four spaces where the file has a tab: nowhere verbatim in the file.
	edited := c.applyToolEdits([]plannedEdit{
		editCall("call_1", "    println(\"hi\")\n", "    println(\"bye\")\n"),
	}, results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v, want the line matcher to place it: %q", edited, results["call_1"])
	}
	if c.editsFuzzy != 1 || c.editsExact != 0 {
		t.Errorf("exact=%d fuzzy=%d, want exact=0 fuzzy=1 — the counter cannot see the "+
			"tier it exists to count", c.editsExact, c.editsFuzzy)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "\tprintln(\"bye\")") {
		t.Errorf("file = %q, want the edit applied with the file's own indentation", got)
	}
}

// And the other side of the control: an exact match must not be counted as a
// guess, or the metric reads high for the wrong reason.
func TestExactEditIsNotCountedAsFuzzy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("func main() {\n\tprintln(\"hi\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)
	c.AddFile("a.txt")

	results := map[string]string{}
	matchFailure := false
	c.applyToolEdits([]plannedEdit{
		editCall("call_1", "\tprintln(\"hi\")\n", "\tprintln(\"bye\")\n"),
	}, results, &matchFailure)

	if c.editsExact != 1 || c.editsFuzzy != 0 {
		t.Errorf("exact=%d fuzzy=%d, want exact=1 fuzzy=0", c.editsExact, c.editsFuzzy)
	}
}

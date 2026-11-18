package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/record"
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

	results := toolResults{}
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

// A file that moved since it was read still takes an edit whose text matches
// it exactly once: the match proves the span is there. A live session lost two
// steps to the old refusal, re-reading files ruff had reformatted elsewhere.
// The result says the file changed, so the model knows the rest of what it
// remembers may not hold.
func TestAnExactEditLandsOnAMovedFileAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	c.shown.note("a.txt", path)
	touchLater(t, path, "a rewritten first line\nhello world\n")

	results := toolResults{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{editCall("call_1", "world", "mars")}, results, &matchFailure)
	if len(edited) != 1 || matchFailure {
		t.Fatalf("edited = %v, reflect = %v; want the exact match applied", edited, matchFailure)
	}
	if got, _ := os.ReadFile(path); string(got) != "a rewritten first line\nhello mars\n" {
		t.Errorf("file = %q, want the edit applied and the outside change kept", got)
	}
	got := results["call_1"].Text
	for _, want := range []string{"Applied", "changed on disk since you read it", "outside this conversation"} {
		if !strings.Contains(got, want) {
			t.Errorf("result = %q, want it to mention %q", got, want)
		}
	}
}

// What a moved file does rule out is the line matcher's fallback, which can
// land on text that has shifted, and a "not found" that sends the model looking
// for a typo it did not make. The text below is not in the file verbatim — its
// indentation differs — so on a file the model had seen as it is, the matcher
// would apply it. On a moved file, the model is asked to read again.
func TestAMovedFileGetsNoFuzzyMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("func f() {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	c.shown.note("a.go", path)
	touchLater(t, path, "// moved\nfunc f() {\n\treturn 1\n}\n")

	results := toolResults{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{{callID: "call_1", path: "a.go", search: "    return 1\n", replace: "    return 2\n"}},
		results, &matchFailure)
	if len(edited) != 0 || !matchFailure {
		t.Fatalf("edited = %v, reflect = %v; want the edit refused", edited, matchFailure)
	}
	got := results["call_1"].Text
	for _, want := range []string{"changed on disk since you read it", "Call read on it again"} {
		if !strings.Contains(got, want) {
			t.Errorf("result = %q, want it to mention %q", got, want)
		}
	}
	if strings.Contains(got, "not found") {
		t.Errorf("result = %q, must not report this as a failed match", got)
	}
}

// An edit that lands on a changed file does not make the file read. It
// matched one span; the rest is still what the model has not seen, so the next
// edit is held to the same rule. Seen live: the harness re-stamped the file
// after its own write, and the next edit, written against the pre-gofmt text,
// fell through to the matcher and came back "not found".
func TestAnEditToAMovedFileDoesNotMarkItRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("func f() {\n\treturn 1\n}\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	c.shown.note("a.go", path)
	touchLater(t, path, "// moved\nfunc f() {\n\treturn 1\n}\n\nvar x = 1\n")

	edit := func(id, search, replace string) (toolResults, bool) {
		results := toolResults{}
		failed := false
		c.applyToolEdits([]plannedEdit{{callID: id, path: "a.go", search: search, replace: replace}}, results, &failed)
		return results, failed
	}
	if r, failed := edit("e1", "var x = 1", "var x = 2"); failed {
		t.Fatalf("the exact edit was refused: %q", r["e1"].Text)
	}
	// Only the line matcher could place this: the indentation differs.
	r, failed := edit("e2", "    return 1\n", "    return 2\n")
	if !failed || !strings.Contains(r["e2"].Text, "Call read on it again") {
		t.Errorf("after an edit to a moved file, a fuzzy edit got %q; want the file still treated as changed", r["e2"].Text)
	}
	// A read is what makes it seen.
	c.shown.note("a.go", path)
	if r, failed := edit("e3", "    return 1\n", "    return 2\n"); failed {
		t.Errorf("after a read, the edit was refused: %q", r["e3"].Text)
	}
}

// The model's own command is the other way a read file moves: gofmt -w, sed -i,
// a formatter behind a check. The result must not tell the model someone else
// did it, which is what it said while the stamps knew only about reads and
// writes.
func TestAMoveAfterACommandDoesNotBlameAnOutsideEdit(t *testing.T) {
	for _, tc := range []struct{ search, want string }{
		{"world", "A command run since then may have changed it"},
		{"planet", "A command run since you read it may have changed it"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "a.txt")
		if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c := toolCoder(t, dir)
		c.shown.note("a.txt", path)
		c.runAndShow(context.Background(), "echo 'hello world, reformatted' > a.txt", 0)
		if got, _ := os.ReadFile(path); !strings.Contains(string(got), "reformatted") {
			t.Fatalf("the command did not change the file, so this test proves nothing: %q", got)
		}

		results := toolResults{}
		matchFailure := false
		c.applyToolEdits([]plannedEdit{editCall("call_1", tc.search, "mars")}, results, &matchFailure)
		got := results["call_1"].Text
		if strings.Contains(got, "This is not a mistake in your edit") || strings.Contains(got, "It was modified outside") {
			t.Errorf("search %q: result = %q, asserts an outside edit when a command may have made the change", tc.search, got)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("search %q: result = %q, want %q", tc.search, got, tc.want)
		}
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
	c.shown.note("a.txt", path)

	results := toolResults{}
	matchFailure := false
	if edited := c.applyToolEdits(
		[]plannedEdit{editCall("call_1", "one", "ONE")},
		results, &matchFailure); len(edited) != 1 {
		t.Fatalf("first edit: edited = %v, want it applied", edited)
	}

	results = toolResults{}
	matchFailure = false
	edited := c.applyToolEdits(
		[]plannedEdit{editCall("call_2", "three", "THREE")},
		results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("second edit: edited = %v, want it applied — the harness's own "+
			"write must not read as a change by somebody else: %q", edited, results["call_2"].Text)
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
	c.shown.note("a.txt", path)
	touchLater(t, path, "changed by somebody else entirely\n")

	results := toolResults{}
	matchFailure := false
	edited := c.applyToolEdits(
		[]plannedEdit{wholeFileWrite("call_1", "a.txt", "written fresh\n")},
		results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v, want write to go through: %q", edited, results["call_1"].Text)
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
	if moved, _ := s.changed("a.txt", path); !moved {
		t.Fatal("the stamp did not notice a change, so this test proves nothing")
	}
	s.forget()
	if moved, _ := s.changed("a.txt", path); moved {
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

	results := toolResults{}
	matchFailure := false
	// Four spaces where the file has a tab: nowhere verbatim in the file.
	edited := c.applyToolEdits([]plannedEdit{
		editCall("call_1", "    println(\"hi\")\n", "    println(\"bye\")\n"),
	}, results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v, want the line matcher to place it: %q", edited, results["call_1"].Text)
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

	results := toolResults{}
	matchFailure := false
	c.applyToolEdits([]plannedEdit{
		editCall("call_1", "\tprintln(\"hi\")\n", "\tprintln(\"bye\")\n"),
	}, results, &matchFailure)

	if c.editsExact != 1 || c.editsFuzzy != 0 {
		t.Errorf("exact=%d fuzzy=%d, want exact=1 fuzzy=0", c.editsExact, c.editsFuzzy)
	}
}

// TestFuzzyAmbiguityIsRefused is the bug M1 caught, at the level it bit.
// Three identical blocks; a search whose indentation is wrong matches all three
// equally well, so the raw occurrence count is zero and the old ambiguity guard
// could not see it. The line matcher took the first and reported success — two
// runs in that trial silently rewrote the wrong function.
func TestFuzzyAmbiguityIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	block := "\tif !ok {\n\t\treturn err\n\t}\n"
	if err := os.WriteFile(path, []byte("A\n"+block+"B\n"+block+"C\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}

	c := toolCoder(t, dir)

	results := toolResults{}
	matchFailure := false
	// Two tabs where the file has one: matches all three blocks, verbatim none.
	edited := c.applyToolEdits([]plannedEdit{
		editCall("call_1", "\t\tif !ok {\n\t\t\treturn err\n\t\t}\n", "\t\tif !ok {\n\t\t\treturn wrapped\n\t\t}\n"),
	}, results, &matchFailure)

	if len(edited) != 0 {
		t.Errorf("edited = %v: one of three identical blocks was picked on the model's behalf", edited)
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "wrapped") {
		t.Error("a coin flip was written to disk and reported as success")
	}
	got := results["call_1"].Text
	if !strings.Contains(got, "more than one place") {
		t.Errorf("result = %q, want it to name the ambiguity rather than report 'not found'", got)
	}
	if !matchFailure {
		t.Error("the model must get a chance to disambiguate")
	}
}

// A loose match tells the model what it got wrong, and every edit leaves a
// record saying how its target was found. In the GPT-6 Luna run behind this,
// four of six loose matches repeated one corrected a step or two before,
// because "Applied the edit" was all the model heard; and naming the six took a
// byte-exact replay, because the record only counted them.
func TestLooseEditIsExplainedAndRecorded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("func f() {\n\tfor {\n\t\tgo g()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	rec := &capture{}
	c.Recorder = rec

	results := toolResults{}
	matchFailure := false
	c.applyToolEdits([]plannedEdit{
		editCall("loose", "\t\t\tgo g()\n", "\t\t\tgo h()\n"),            // a tab too many
		editCall("exact", "func f() {\n", "func F() {\n"),                // verbatim
		editCall("absent", "\tnothing like this\n", "\tstill nothing\n"), // not in the file
	}, results, &matchFailure)

	got := results["loose"].Text
	if !strings.HasPrefix(got, "Applied the edit to") ||
		!strings.Contains(got, "line 3 has 2 tabs, and you sent 3 tabs") {
		t.Errorf("loose result = %q; want success plus the indentation it corrected", got)
	}
	if strings.Contains(results["exact"].Text, "indented") {
		t.Errorf("exact result = %q; an exact match has nothing to explain", results["exact"].Text)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "\t\tgo h()") {
		t.Errorf("file = %q; want the loose edit at the file's own indentation", b)
	}

	outcomes := map[string]record.Record{}
	for _, r := range rec.recs {
		if r.Type == record.KindEdit {
			outcomes[r.ToolCallID] = r
		}
	}
	for id, want := range map[string]string{"loose": "loose", "exact": "exact", "absent": "not_found"} {
		r, ok := outcomes[id]
		if !ok || r.Outcome != want || r.Path != "a.txt" {
			t.Errorf("edit record for %s = %+v (present %v); want outcome %q on a.txt", id, r, ok, want)
		}
	}
	if !strings.Contains(outcomes["loose"].Summary, "you sent 3 tabs") {
		t.Errorf("the loose record's summary = %q; want the same explanation the model got", outcomes["loose"].Summary)
	}
}

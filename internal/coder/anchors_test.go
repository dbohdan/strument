package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/anchor"
	"dbohdan.com/strument/internal/llm"
)

func testRegistry() *anchorRegistry {
	return newAnchorRegistry(&anchor.FixedSupply{Bytes: []byte{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9}})
}

// The property the whole format rests on: an edit somewhere else in the file
// does not move an anchor. If this fails, an anchor is a line number with extra
// steps and extra tokens.
func TestAnchorsSurviveAnEditElsewhere(t *testing.T) {
	r := testRegistry()
	before := []string{"package p", "", "func a() {}", "", "func b() {}"}
	ids := r.sync("f.go", before)

	// An insertion above shifts every line below it.
	after := []string{"package p", "", "// new", "func a() {}", "", "func b() {}"}
	ids2 := r.sync("f.go", after)

	if ids[0] != ids2[0] {
		t.Errorf("the unchanged first line changed identity: %q -> %q", ids[0], ids2[0])
	}
	if ids[2] != ids2[3] {
		t.Errorf("func a() moved from line 3 to line 4 and lost its anchor: %q -> %q", ids[2], ids2[3])
	}
	if ids[4] != ids2[5] {
		t.Errorf("func b() lost its anchor: %q -> %q", ids[4], ids2[5])
	}
	if ids2[2] == ids[2] {
		t.Error("the inserted line reused an existing anchor")
	}
	if line, ok := r.resolve("f.go", ids[4]); !ok || line != 5 {
		t.Errorf("resolve after the shift = %d, %v; want line 5", line, ok)
	}
}

// Identical lines must not share an anchor, or an edit addressed to one of them
// is ambiguous again — the exact failure this format removes.
func TestIdenticalLinesGetDistinctAnchors(t *testing.T) {
	r := testRegistry()
	ids := r.sync("f.go", []string{"\t}", "\t}", "\t}", "", ""})
	seen := map[anchor.Anchor]bool{}
	for i, a := range ids {
		if seen[a] {
			t.Fatalf("line %d reuses anchor %q", i, a)
		}
		seen[a] = true
	}
	// And they stay distinct, and stay themselves, across a re-sync.
	again := r.sync("f.go", []string{"\t}", "\t}", "\t}", "", ""})
	for i := range ids {
		if ids[i] != again[i] {
			t.Errorf("line %d changed identity on an unchanged re-read: %q -> %q", i, ids[i], again[i])
		}
	}
}

// A line the user edited outside the conversation gets a new anchor, so the
// model is never handed an identity pointing at content it has not seen.
func TestAChangedLineIsReminted(t *testing.T) {
	r := testRegistry()
	ids := r.sync("f.go", []string{"a", "b", "c"})
	ids2 := r.sync("f.go", []string{"a", "CHANGED", "c"})

	if ids[1] == ids2[1] {
		t.Error("a rewritten line kept its anchor: the model would edit content it never saw")
	}
	if ids[0] != ids2[0] || ids[2] != ids2[2] {
		t.Error("untouched lines were re-minted")
	}
	if _, ok := r.resolve("f.go", ids[1]); ok {
		t.Error("the retired anchor still resolves")
	}
}

// An anchor the registry does not know is not resolved to anything. "Not found"
// is the answer; guessing is what the format exists to stop.
func TestUnknownAnchorsDoNotResolve(t *testing.T) {
	r := testRegistry()
	r.sync("f.go", []string{"a", "b"})
	for _, a := range []anchor.Anchor{"copper-otter", "not-here"} {
		if _, ok := r.resolve("f.go", a); ok {
			t.Errorf("resolved an anchor that was never minted: %q", a)
		}
	}
	if _, ok := r.resolve("other.go", "copper-otter"); ok {
		t.Error("resolved against a file that was never read")
	}
	if r.known("other.go") {
		t.Error("known() is true for a file that was never read")
	}
}

// The read row: anchor, tab, line verbatim — indentation included, no bar.
func TestRenderAnchored(t *testing.T) {
	r := testRegistry()
	lines := []string{"func f() {", "\treturn nil", "}"}
	got := renderAnchored(r.sync("f.go", lines), lines, false)
	for i, row := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		id, content, found := strings.Cut(row, "\t")
		if !found {
			t.Fatalf("row %d has no tab separator: %q", i, row)
		}
		if _, ok := anchor.Parse(id); !ok {
			t.Errorf("row %d does not start with an anchor: %q", i, row)
		}
		if content != lines[i] {
			t.Errorf("row %d content = %q, want the line verbatim %q", i, content, lines[i])
		}
	}
	if strings.Contains(got, "║") {
		t.Error("the heavy bar is back: it is 3 tokens against a tab's 1, twice a row")
	}
}

// anchoredCoder is a coder with anchored edits on and a fixed anchor supply, so
// the identities are the same every run.
func anchoredCoder(t *testing.T, dir string) *Coder {
	t.Helper()
	c := toolCoder(t, dir)
	c.AnchoredEdits = true
	c.anchors = testRegistry()
	return c
}

// The reason arm D was built. Three identical blocks: under old_string this is
// ambiguous and refused, and under a whitespace-mismatched search it used to
// silently edit the first. An anchor names one line, so neither can happen.
func TestAnchoredEditIsUnambiguousAmongIdenticalBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.go")
	block := "\tif !ok {\n\t\treturn err\n\t}\n"
	if err := os.WriteFile(path, []byte("A\n"+block+"B\n"+block+"C\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}
	c := anchoredCoder(t, dir)
	c.AddFile("h.go")

	// Read the file the way the model would, and take the anchor of the second
	// block's first line (line index 5).
	rows := c.anchorRows("h.go", 0, 12)
	ids := strings.Split(strings.TrimRight(rows, "\n"), "\n")
	if len(ids) < 8 {
		t.Fatalf("read gave %d rows, want the whole file", len(ids))
	}
	second, _, _ := strings.Cut(ids[5], "\t")
	third, _, _ := strings.Cut(ids[8], "\t")
	if second == third {
		t.Fatal("identical lines share an anchor, so this proves nothing")
	}

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{{
		callID: "call_1", path: "h.go", anchor: second, endAnchor: third,
		replace: "\tif !ok {\n\t\treturn fmt.Errorf(\"b: %w\", err)\n\t}\n",
	}}, results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v, want the anchored edit applied: %q", edited, results["call_1"])
	}
	got, _ := os.ReadFile(path)
	if n := strings.Count(string(got), "fmt.Errorf"); n != 1 {
		t.Errorf("the replacement landed %d times, want exactly the anchored range:\n%s", n, got)
	}
	// The first block is untouched: this is what "the harness picked the first
	// of three" looked like, and it must not happen.
	if !strings.HasPrefix(string(got), "A\n\tif !ok {\n\t\treturn err\n\t}\n") {
		t.Errorf("the first block was disturbed:\n%s", got)
	}
	if !strings.Contains(results["call_1"], "New anchors") {
		t.Errorf("result = %q, want the digest so the next edit need not re-read", results["call_1"])
	}
}

// An anchor whose line changed no longer resolves, and the model is told to
// read again rather than having the harness find something nearby.
func TestStaleAnchorIsRefusedNotGuessed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := anchoredCoder(t, dir)
	c.AddFile("a.txt")
	rows := c.anchorRows("a.txt", 0, 3)
	twoAnchor, _, _ := strings.Cut(strings.Split(rows, "\n")[1], "\t")

	// Somebody rewrites the line the anchor names.
	if err := os.WriteFile(path, []byte("one\nTWO REWRITTEN\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.anchorRows("a.txt", 0, 3) // a fresh read re-mints the changed line

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		{callID: "call_1", path: "a.txt", anchor: twoAnchor, replace: "second\n"},
	}, results, &matchFailure)

	if len(edited) != 0 {
		t.Errorf("edited = %v, want the stale anchor refused", edited)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "TWO REWRITTEN") {
		t.Errorf("the rewritten line was overwritten via a retired anchor:\n%s", got)
	}
	for _, want := range []string{"does not name a line", "Read it again"} {
		if !strings.Contains(results["call_1"], want) {
			t.Errorf("result = %q, want %q", results["call_1"], want)
		}
	}
}

// Two anchored edits to one file in a turn compose: the first's digest gives
// the identities the second addresses, with no re-read between them. That
// round trip is what anchors buy over line numbers, so it is asserted.
func TestTwoAnchoredEditsComposeWithoutAReread(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := anchoredCoder(t, dir)
	c.AddFile("a.txt")
	rows := strings.Split(strings.TrimRight(c.anchorRows("a.txt", 0, 4), "\n"), "\n")
	first, _, _ := strings.Cut(rows[0], "\t")
	last, _, _ := strings.Cut(rows[3], "\t")

	results := map[string]string{}
	matchFailure := false
	// One batch, two calls. The first inserts a line, which shifts every line
	// below it — the case where a line number would have gone stale.
	edited := c.applyToolEdits([]plannedEdit{
		{callID: "call_1", path: "a.txt", anchor: first, replace: "ALPHA\nextra\n"},
		{callID: "call_2", path: "a.txt", anchor: last, replace: "DELTA\n"},
	}, results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v: %q / %q", edited, results["call_1"], results["call_2"])
	}
	want := "ALPHA\nextra\nbeta\ngamma\nDELTA\n"
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

// Anchored edits are off by default, and the read format is then unchanged.
func TestAnchoredEditsAreOffByDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	if c.AnchoredEdits {
		t.Error("anchored edits default to on; the trial that would justify that has not run")
	}
	if rows := c.anchorRows("a.txt", 0, 1); rows != "" {
		t.Errorf("the read format changed with the setting off: %q", rows)
	}
}

// The two ways of saying where must not be offered together: a schema that
// accepts both accepts a disagreement.
func TestAnchorAndOldStringAreMutuallyExclusive(t *testing.T) {
	_, msg := parseEditArgs(llm.ToolCall{
		ID: "c1", Name: toolEdit,
		Arguments: `{"path":"a.txt","anchor":"copper-otter","old_string":"x","new_string":"y"}`,
	})
	if msg == "" {
		t.Error("sending both an anchor and an old_string was accepted")
	}
	if _, msg := parseEditArgs(llm.ToolCall{
		ID: "c2", Name: toolEdit,
		Arguments: `{"path":"a.txt","end_anchor":"copper-otter","new_string":"y"}`,
	}); msg == "" {
		t.Error("an end_anchor with no anchor was accepted")
	}
}

func indentColumnCoder(t *testing.T, dir string) *Coder {
	t.Helper()
	c := anchoredCoder(t, dir)
	c.IndentColumn = true
	return c
}

// The read row under arm E: anchor, indent in words, text with no leading
// whitespace of its own.
func TestIndentColumnReadRow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("func f() {\n\tif x {\n\t\treturn\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indentColumnCoder(t, dir)
	rows := strings.Split(strings.TrimRight(c.anchorRows("a.go", 0, 5), "\n"), "\n")
	want := []string{"0 spaces\tfunc f() {", "1 tab\tif x {", "2 tabs\treturn", "1 tab\t}", "0 spaces\t}"}
	for i, row := range rows {
		_, rest, _ := strings.Cut(row, "\t")
		if rest != want[i] {
			t.Errorf("row %d = %q, want %q", i, rest, want[i])
		}
	}
}

// The point of arm E: the model states indentation, so it lands exactly as
// stated. This is the case phase 1 measured going wrong 30 times in 72.
func TestIndentColumnPutsTheStatedIndentationOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("func f() {\n\tif x {\n\t\treturn\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indentColumnCoder(t, dir)
	c.AddFile("a.go")
	rows := strings.Split(strings.TrimRight(c.anchorRows("a.go", 0, 5), "\n"), "\n")
	id, _, _ := strings.Cut(rows[2], "\t") // the "return" line

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		{callID: "c1", path: "a.go", anchor: id, replace: "2 tabs\treturn nil\n"},
	}, results, &matchFailure)

	if len(edited) != 1 {
		t.Fatalf("edited = %v: %q", edited, results["c1"])
	}
	got, _ := os.ReadFile(path)
	if want := "func f() {\n\tif x {\n\t\treturn nil\n\t}\n}\n"; string(got) != want {
		t.Errorf("file = %q\nwant %q", got, want)
	}
}

// An indentation the model cannot name correctly is refused rather than
// written. This is the safety net anchoring removed, put back: under arm D the
// bad whitespace went straight to disk.
func TestMalformedIndentIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("func f() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indentColumnCoder(t, dir)
	c.AddFile("a.go")
	rows := strings.Split(strings.TrimRight(c.anchorRows("a.go", 0, 3), "\n"), "\n")
	id, _, _ := strings.Cut(rows[1], "\t")

	for name, replace := range map[string]string{
		"no tab":         "\treturn nil\n",
		"bad agreement":  "1 tabs\treturn nil\n",
		"literal indent": "\t\treturn nil\n",
		"unknown unit":   "2 indents\treturn nil\n",
	} {
		results := map[string]string{}
		matchFailure := false
		edited := c.applyToolEdits([]plannedEdit{
			{callID: "c1", path: "a.go", anchor: id, replace: replace},
		}, results, &matchFailure)
		if len(edited) != 0 {
			t.Errorf("%s: edited = %v, want it refused", name, edited)
		}
		if got, _ := os.ReadFile(path); string(got) != "func f() {\n\treturn\n}\n" {
			t.Errorf("%s: the file changed: %q", name, got)
		}
		if !matchFailure {
			t.Errorf("%s: the model must get a chance to restate it", name)
		}
	}
}

// The column is only offered alongside anchors, and the schema says so — a
// model told to name indentation while the harness expects literal whitespace
// would produce exactly the corruption this is meant to prevent.
func TestIndentColumnSchemaDescribesTheGrammar(t *testing.T) {
	plain := editTools(true, false)
	col := editTools(true, true)
	find := func(defs []llm.ToolDef) string {
		for _, d := range defs {
			if d.Name != toolEdit {
				continue
			}
			props, ok := d.Parameters["properties"].(map[string]any)
			if !ok {
				t.Fatal("the edit schema has no properties map")
			}
			ns, ok := props["new_string"].(map[string]any)
			if !ok {
				t.Fatal("the edit schema has no new_string property")
			}
			desc, ok := ns["description"].(string)
			if !ok {
				t.Fatal("new_string has no description")
			}
			return desc
		}
		return ""
	}
	if strings.Contains(find(plain), "indentation in words") {
		t.Error("the plain anchored schema describes a column it does not have")
	}
	for _, want := range []string{"indentation in words", "2 tabs", "0 spaces", "never as actual spaces"} {
		if !strings.Contains(find(col), want) {
			t.Errorf("the indent-column schema does not mention %q", want)
		}
	}
}

// Stating the indentation and then typing it as well lands as both. Phase 1's
// arm E measured models doing exactly that — "3 tabs\t\t\treturn nil" — and it
// is worse than having no column, because the model believes it has been
// explicit and the harness silently doubles it.
func TestIndentColumnRejectsDoubledIndentation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	const before = "func f() {\n\tif x {\n\t\treturn\n\t}\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indentColumnCoder(t, dir)
	c.AddFile("a.go")
	rows := strings.Split(strings.TrimRight(c.anchorRows("a.go", 0, 5), "\n"), "\n")
	id, _, _ := strings.Cut(rows[2], "\t")

	results := map[string]string{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		{callID: "c1", path: "a.go", anchor: id, replace: "2 tabs\t\t\treturn nil\n"},
	}, results, &matchFailure)

	if len(edited) != 0 {
		t.Errorf("edited = %v: the indentation was stated and typed, and would land twice", edited)
	}
	if got, _ := os.ReadFile(path); string(got) != before {
		t.Errorf("file changed: %q", got)
	}
	if !strings.Contains(results["c1"], "belongs in the column") {
		t.Errorf("result = %q, want it to say where indentation goes", results["c1"])
	}
	// And a correctly formed row still works, so this is a restriction, not a wall.
	results, matchFailure = map[string]string{}, false
	if edited := c.applyToolEdits([]plannedEdit{
		{callID: "c2", path: "a.go", anchor: id, replace: "2 tabs\treturn nil\n"},
	}, results, &matchFailure); len(edited) != 1 {
		t.Fatalf("a well-formed row was refused: %q", results["c2"])
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "\t\treturn nil\n") {
		t.Errorf("file = %q, want exactly two tabs", got)
	}
}

// Transliterated from aider tests/basic/test_editblock.py @ 5dc9490.
// The three coder-level cases (test_full_edit, test_full_edit_dry_run,
// test_create_new_file_with_other_file_in_chat) are ported against
// ApplyEdits here and re-exercised end-to-end in phase 5.

package editblock

import (
	"strings"
	"testing"
)

func edits(t *testing.T, blocks []Block) []Edit {
	t.Helper()
	var out []Edit
	for _, b := range blocks {
		if !b.IsShell {
			out = append(out, b.Edit)
		}
	}
	return out
}

func TestFindFilename(t *testing.T) {
	fence := Fence{"```", "```"}
	valid := []string{"file1.py", "file2.py", "dir/file3.py", `\windows\__init__.py`}

	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"single line", []string{"file1.py", "```"}, "file1.py"},
		{"in fence", []string{"```python", "file3.py", "```"}, "dir/file3.py"},
		{"no valid filename", []string{"```", "invalid_file.py", "```"}, "invalid_file.py"},
		{"multiple fences", []string{"```python", "file1.py", "```", "```", "file2.py", "```"}, "file2.py"},
		{"extra characters", []string{"# file1.py", "```"}, "file1.py"},
		{"fuzzy", []string{"file1_py", "```"}, "file1.py"},
		{"fuzzy windows", []string{`\windows__init__.py`, "```"}, `\windows\__init__.py`},
	}
	for _, c := range cases {
		if got := FindFilename(c.lines, fence, valid); got != c.want {
			t.Errorf("%s: FindFilename(%q) = %q, want %q", c.name, c.lines, got, c.want)
		}
	}
}

func TestStripQuotedWrapping(t *testing.T) {
	input := "filename.ext\n```\nWe just want this content\nNot the filename and triple quotes\n```"
	want := "We just want this content\nNot the filename and triple quotes\n"
	if got := StripQuotedWrapping(input, "filename.ext", DefaultFence); got != want {
		t.Errorf("got %q", got)
	}
}

func TestStripQuotedWrappingNoFilename(t *testing.T) {
	input := "```\nWe just want this content\nNot the triple quotes\n```"
	want := "We just want this content\nNot the triple quotes\n"
	if got := StripQuotedWrapping(input, "", DefaultFence); got != want {
		t.Errorf("got %q", got)
	}
}

func TestStripQuotedWrappingNoWrapping(t *testing.T) {
	input := "We just want this content\nNot the triple quotes\n"
	if got := StripQuotedWrapping(input, "", DefaultFence); got != input {
		t.Errorf("got %q", got)
	}
}

func TestFindOriginalUpdateBlocks(t *testing.T) {
	edit := `
Here's the change:

` + "```text" + `
foo.txt
<<<<<<< SEARCH
Two
=======
Tooooo
>>>>>>> REPLACE
` + "```" + `

Hope you like it!
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	if len(got) != 1 || got[0] != (Edit{"foo.txt", "Two\n", "Tooooo\n"}) {
		t.Errorf("got %+v", got)
	}
}

func TestFindOriginalUpdateBlocksQuoteBelowFilename(t *testing.T) {
	edit := `
Here's the change:

foo.txt
` + "```text" + `
<<<<<<< SEARCH
Two
=======
Tooooo
>>>>>>> REPLACE
` + "```" + `

Hope you like it!
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	if len(got) != 1 || got[0] != (Edit{"foo.txt", "Two\n", "Tooooo\n"}) {
		t.Errorf("got %+v", got)
	}
}

func TestFindOriginalUpdateBlocksUnclosed(t *testing.T) {
	edit := `
Here's the change:

` + "```text" + `
foo.txt
<<<<<<< SEARCH
Two
=======
Tooooo


oops!
`
	_, err := FindBlocks(edit, DefaultFence, nil)
	if err == nil || !strings.Contains(err.Error(), "Expected `>>>>>>> REPLACE` or `=======`") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindOriginalUpdateBlocksMissingFilename(t *testing.T) {
	edit := `
Here's the change:

` + "```text" + `
<<<<<<< SEARCH
Two
=======
Tooooo


oops!
>>>>>>> REPLACE
`
	_, err := FindBlocks(edit, DefaultFence, nil)
	if err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindOriginalUpdateBlocksNoFinalNewline(t *testing.T) {
	edit := `
aider/coder.py
<<<<<<< SEARCH
            self.console.print("[red]^C again to quit")
=======
            self.io.tool_error("^C again to quit")
>>>>>>> REPLACE

aider/coder.py
<<<<<<< SEARCH
            self.io.tool_error("Malformed ORIGINAL/UPDATE blocks, retrying...")
            self.io.tool_error(err)
=======
            self.io.tool_error("Malformed ORIGINAL/UPDATE blocks, retrying...")
            self.io.tool_error(str(err))
>>>>>>> REPLACE

aider/coder.py
<<<<<<< SEARCH
            self.console.print("[red]Unable to get commit message from gpt-3.5-turbo. Use /commit to try again.\n")
=======
            self.io.tool_error("Unable to get commit message from gpt-3.5-turbo. Use /commit to try again.")
>>>>>>> REPLACE

aider/coder.py
<<<<<<< SEARCH
            self.console.print("[red]Skipped commit.")
=======
            self.io.tool_error("Skipped commit.")
>>>>>>> REPLACE`
	if _, err := FindBlocks(edit, DefaultFence, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIncompleteEditBlockMissingFilename(t *testing.T) {
	edit := `
No problem! Here are the changes to patch ` + "`subprocess.check_output`" + ` instead of ` + "`subprocess.run`" + ` in both tests:

` + "```python" + `
tests/test_repomap.py
<<<<<<< SEARCH
    def test_check_for_ctags_failure(self):
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = Exception("ctags not found")
=======
    def test_check_for_ctags_failure(self):
        with patch("subprocess.check_output") as mock_check_output:
            mock_check_output.side_effect = Exception("ctags not found")
>>>>>>> REPLACE

<<<<<<< SEARCH
    def test_check_for_ctags_success(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = CompletedProcess(args=["ctags", "--version"], returncode=0, stdout='''{
  "_type": "tag",
  "name": "status",
  "path": "aider/main.py",
  "pattern": "/^    status = main()$/",
  "kind": "variable"
}''')
=======
    def test_check_for_ctags_success(self):
        with patch("subprocess.check_output") as mock_check_output:
            mock_check_output.return_value = '''{
  "_type": "tag",
  "name": "status",
  "path": "aider/main.py",
  "pattern": "/^    status = main()$/",
  "kind": "variable"
}'''
>>>>>>> REPLACE
` + "```" + `

These changes replace the ` + "`subprocess.run`" + ` patches with ` + "`subprocess.check_output`" + ` patches in both ` + "`test_check_for_ctags_failure`" + ` and ` + "`test_check_for_ctags_success`" + ` tests.
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	if len(got) != 2 {
		t.Fatalf("want 2 edits, got %d", len(got))
	}
	if got[0].Path != "tests/test_repomap.py" || got[1].Path != "tests/test_repomap.py" {
		t.Errorf("paths = %q, %q", got[0].Path, got[1].Path)
	}
}

func TestReplacePartWithMissingVariedLeadingWhitespace(t *testing.T) {
	whole := "\n    line1\n    line2\n        line3\n    line4\n"
	part := "line2\n    line3\n"
	replace := "new_line2\n    new_line3\n"
	want := "\n    line1\n    new_line2\n        new_line3\n    line4\n"
	got, ok := ReplaceMostSimilarChunk(whole, part, replace)
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestReplacePartWithMissingLeadingWhitespace(t *testing.T) {
	got, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line3\n",
		"line1\nline2\n",
		"new_line1\nnew_line2\n")
	want := "    new_line1\n    new_line2\n    line3\n"
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestReplaceMultipleMatches(t *testing.T) {
	// Only the first occurrence is replaced.
	got, ok := ReplaceMostSimilarChunk("line1\nline2\nline1\nline3\n", "line1\n", "new_line\n")
	want := "new_line\nline2\nline1\nline3\n"
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestReplaceMultipleMatchesMissingWhitespace(t *testing.T) {
	got, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line1\n    line3\n",
		"line1\n", "new_line\n")
	want := "    new_line\n    line2\n    line1\n    line3\n"
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestReplacePartWithJustSomeMissingLeadingWhitespace(t *testing.T) {
	got, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line3\n",
		" line1\n line2\n",
		" new_line1\n     new_line2\n")
	want := "    new_line1\n        new_line2\n    line3\n"
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestReplacePartWithMissingLeadingWhitespaceIncludingBlankLine(t *testing.T) {
	// Issue #25: a blank line in the part must not defeat the uniform
	// outdent.
	got, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line3\n",
		"\n  line1\n  line2\n",
		"  new_line1\n  new_line2\n")
	want := "    new_line1\n    new_line2\n    line3\n"
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestFindOriginalUpdateBlocksMultipleSameFile(t *testing.T) {
	edit := `
Here's the change:

` + "```text" + `
foo.txt
<<<<<<< SEARCH
one
=======
two
>>>>>>> REPLACE

...

<<<<<<< SEARCH
three
=======
four
>>>>>>> REPLACE
` + "```" + `

Hope you like it!
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	want := []Edit{
		{"foo.txt", "one\n", "two\n"},
		{"foo.txt", "three\n", "four\n"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v", got)
	}
}

func TestDeepseekCoderV2FilenameMangling(t *testing.T) {
	edit := `
Here's the change:

 ` + "```python" + `
foo.txt
` + "```" + `
` + "```python" + `
<<<<<<< SEARCH
one
=======
two
>>>>>>> REPLACE
` + "```" + `

Hope you like it!
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	if len(got) != 1 || got[0] != (Edit{"foo.txt", "one\n", "two\n"}) {
		t.Errorf("got %+v", got)
	}
}

func TestNewFileCreatedInSameFolder(t *testing.T) {
	edit := `
Here's the change:

path/to/a/file2.txt
` + "```python" + `
<<<<<<< SEARCH
=======
three
>>>>>>> REPLACE
` + "```" + `

another change

path/to/a/file1.txt
` + "```python" + `
<<<<<<< SEARCH
one
=======
two
>>>>>>> REPLACE
` + "```" + `

Hope you like it!
`
	blocks, err := FindBlocks(edit, DefaultFence, []string{"path/to/a/file1.txt"})
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	want := []Edit{
		{"path/to/a/file2.txt", "", "three\n"},
		{"path/to/a/file1.txt", "one\n", "two\n"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v", got)
	}
}

func TestQuadBackticksWithTriplesInLLMReply(t *testing.T) {
	// Issue #2879: the fence is quad backticks but the LLM replies with
	// triples anyway.
	edit := `
Here's the change:

foo.txt
` + "```text" + `
<<<<<<< SEARCH
=======
Tooooo
>>>>>>> REPLACE
` + "```" + `

Hope you like it!
`
	blocks, err := FindBlocks(edit, Fence{"````", "````"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	if len(got) != 1 || got[0] != (Edit{"foo.txt", "", "Tooooo\n"}) {
		t.Errorf("got %+v", got)
	}
}

func TestShLanguageIdentifier(t *testing.T) {
	// Issue #3785: a shell-fenced edit block must parse as an edit, not a
	// shell command.
	edit := `
Here's a shell script:

` + "```sh" + `
test_hello.sh
<<<<<<< SEARCH
=======
#!/bin/bash
# Check if exactly one argument is provided
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <argument>" >&2
    exit 1
fi

# Echo the first argument
echo "$1"

exit 0
>>>>>>> REPLACE
` + "```" + `
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	if len(got) != 1 {
		t.Fatalf("want 1 edit, got %d (%+v)", len(got), blocks)
	}
	if got[0].Path != "test_hello.sh" || got[0].Search != "" {
		t.Errorf("got %+v", got[0])
	}
	for _, s := range []string{"#!/bin/bash", `if [ "$#" -ne 1 ];`, `echo "Usage: $0 <argument>"`, "exit 1", `echo "$1"`, "exit 0"} {
		if !strings.Contains(got[0].Replace, s) {
			t.Errorf("replace content missing %q", s)
		}
	}
}

func TestCsharpLanguageIdentifier(t *testing.T) {
	edit := `
Here's a C# code change:

` + "```csharp" + `
Program.cs
<<<<<<< SEARCH
Console.WriteLine("Hello World!");
=======
Console.WriteLine("Hello, C# World!");
>>>>>>> REPLACE
` + "```" + `
`
	blocks, err := FindBlocks(edit, DefaultFence, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := edits(t, blocks)
	want := Edit{"Program.cs", "Console.WriteLine(\"Hello World!\");\n", "Console.WriteLine(\"Hello, C# World!\");\n"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v", got)
	}
}

// mapReader implements FileReader over a map.
type mapReader map[string]string

func (m mapReader) ReadFile(path string) (string, bool) {
	c, ok := m[path]
	return c, ok
}

// ApplyEdits analog of test_full_edit.
func TestApplyEditsFullEdit(t *testing.T) {
	files := mapReader{"file1.txt": "one\ntwo\nthree\n"}
	res := ApplyEdits([]Edit{{"file1.txt", "two\n", "new\n"}}, []string{"file1.txt"}, files, DefaultFence)
	if res.Report != "" || len(res.Failed) != 0 {
		t.Fatalf("failed: %+v\n%s", res.Failed, res.Report)
	}
	if got := res.Writes["file1.txt"]; got != "one\nnew\nthree\n" {
		t.Errorf("got %q", got)
	}
}

// ApplyEdits analog of test_create_new_file_with_other_file_in_chat
// (issue #2258): a new-file block with an unrelated file in the chat must
// not dump its content into the chat file.
func TestApplyEditsCreateNewFileWithOtherFileInChat(t *testing.T) {
	files := mapReader{"file.txt": "one\ntwo\nthree\n"}
	res := ApplyEdits([]Edit{{"newfile.txt", "", "creating a new file\n"}}, []string{"file.txt"}, files, DefaultFence)
	if res.Report != "" || len(res.Failed) != 0 {
		t.Fatalf("failed: %+v\n%s", res.Failed, res.Report)
	}
	if got := res.Writes["newfile.txt"]; got != "creating a new file\n" {
		t.Errorf("newfile.txt = %q", got)
	}
	if _, touched := res.Writes["file.txt"]; touched {
		t.Error("file.txt must not be modified")
	}
}

// Cross-file retry: the right block under the wrong filename lands in the
// chat file that matches.
func TestApplyEditsCrossFileRetry(t *testing.T) {
	files := mapReader{
		"a.txt": "alpha\n",
		"b.txt": "beta\n",
	}
	res := ApplyEdits([]Edit{{"a.txt", "beta\n", "gamma\n"}}, []string{"a.txt", "b.txt"}, files, DefaultFence)
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %+v\n%s", res.Failed, res.Report)
	}
	if got := res.Writes["b.txt"]; got != "gamma\n" {
		t.Errorf("b.txt = %q", got)
	}
	if len(res.Applied) != 1 || res.Applied[0].Path != "b.txt" {
		t.Errorf("applied = %+v", res.Applied)
	}
}

func TestFailureReportShape(t *testing.T) {
	files := mapReader{"foo.txt": "one\ntwo\nthree\n"}
	res := ApplyEdits([]Edit{
		{"foo.txt", "two\n", "TWO\n"},
		{"foo.txt", "seven\n", "SEVEN\n"},
	}, []string{"foo.txt"}, files, DefaultFence)
	if len(res.Applied) != 1 || len(res.Failed) != 1 {
		t.Fatalf("applied=%d failed=%d", len(res.Applied), len(res.Failed))
	}
	for _, want := range []string{
		"# 1 SEARCH/REPLACE block failed to match!",
		"## SearchReplaceNoExactMatch: This SEARCH block failed to exactly match lines in foo.txt",
		"<<<<<<< SEARCH\nseven\n=======\nSEVEN\n>>>>>>> REPLACE",
		"The SEARCH section must exactly match an existing block of lines including all white space, comments, indentation, docstrings, etc",
		"# The other 1 SEARCH/REPLACE block were applied successfully.",
		"Don't re-send them.",
		"Just reply with fixed versions of the block above that failed to match.",
	} {
		if !strings.Contains(res.Report, want) {
			t.Errorf("report missing %q\nreport:\n%s", want, res.Report)
		}
	}
}

func TestDotDotDots(t *testing.T) {
	whole := "top\nmid\nbot\n"
	part := "top\n...\nbot\n"
	replace := "TOP\n...\nBOT\n"
	got, ok := ReplaceMostSimilarChunk(whole, part, replace)
	if !ok || got != "TOP\nmid\nBOT\n" {
		t.Errorf("got %q, %v", got, ok)
	}
	// Unpaired dots are a no-match, not a panic.
	if _, ok := ReplaceMostSimilarChunk(whole, "top\n...\nbot\n", "TOP\nBOT\n"); ok {
		t.Error("unpaired dots must not match")
	}
}

// Transliterated from aider tests/basic/test_editblock.py @ 5dc9490.
//
// Only the matcher cases remain. The parsing cases (find_original_update_blocks
// and its filename handling) and the batch-planner cases went with the text edit
// formats they proved: an edit now arrives with a typed path, so there is no
// prose to parse and no filename to recover from it. What is left is what was
// never format-specific — landing a replacement whose whitespace the model
// reproduced imperfectly.

package editblock

import (
	"testing"
)

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

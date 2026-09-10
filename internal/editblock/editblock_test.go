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
	"strings"
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
	got, _, ok := ReplaceMostSimilarChunk(whole, part, replace)
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestReplacePartWithMissingLeadingWhitespace(t *testing.T) {
	got, _, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line3\n",
		"line1\nline2\n",
		"new_line1\nnew_line2\n")
	want := "    new_line1\n    new_line2\n    line3\n"
	if !ok || got != want {
		t.Errorf("got %q, %v", got, ok)
	}
}

// A deliberate divergence from aider, in both of the tests below. aider
// replaces the first of several matching runs; Strument declines and asks the
// model to say which it means.
//
// Strument already made that choice for the exact path — see the uniqueness
// check in coder/tools.go and its comment about a harness "returning success on
// an underconstrained transformation". These two tests pinned the *fuzzy* path
// still doing what that comment forbids, and it is not a hypothetical:
// doc/experiments/2026-09-anchored-edit/m1.md caught it rewriting the wrong one
// of three identical HTTP handlers, twice, reporting success both times. The
// model's search was under-specified; taking a guess on its behalf is what
// turns that into a silent wrong write.
func TestReplaceMultipleMatchesIsAmbiguous(t *testing.T) {
	got, ambiguous, ok := ReplaceMostSimilarChunk(
		"line1\nline2\nline1\nline3\n", "line1\n", "new_line\n")
	if ok {
		t.Errorf("replaced one of two identical runs: %q", got)
	}
	if !ambiguous {
		t.Error("want ambiguous, so the caller can say which failure this is")
	}
}

func TestReplaceMultipleMatchesMissingWhitespaceIsAmbiguous(t *testing.T) {
	// The indentation is wrong *and* the run occurs twice: the exact-occurrence
	// count the caller checks is zero here, so this path is the only thing
	// standing between the model and a coin flip.
	got, ambiguous, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line1\n    line3\n",
		"line1\n", "new_line\n")
	if ok {
		t.Errorf("replaced one of two whitespace-equivalent runs: %q", got)
	}
	if !ambiguous {
		t.Error("want ambiguous")
	}
}

// The single-match case still works, which is what makes the two above a
// restriction rather than a removal.
func TestReplaceSingleMatchMissingWhitespaceStillWorks(t *testing.T) {
	got, ambiguous, ok := ReplaceMostSimilarChunk(
		"    line1\n    line2\n    line3\n", "line1\n", "new_line\n")
	if !ok || ambiguous {
		t.Fatalf("a unique whitespace-equivalent run must still match: %q amb=%v", got, ambiguous)
	}
	if want := "    new_line\n    line2\n    line3\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReplacePartWithJustSomeMissingLeadingWhitespace(t *testing.T) {
	got, _, ok := ReplaceMostSimilarChunk(
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
	got, _, ok := ReplaceMostSimilarChunk(
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
	got, _, ok := ReplaceMostSimilarChunk(whole, part, replace)
	if !ok || got != "TOP\nmid\nBOT\n" {
		t.Errorf("got %q, %v", got, ok)
	}
	// Unpaired dots are a no-match, not a panic.
	if _, _, ok := ReplaceMostSimilarChunk(whole, "top\n...\nbot\n", "TOP\nBOT\n"); ok {
		t.Error("unpaired dots must not match")
	}
}

// TestDoReplaceMatchesASubstring pins the contract the edit tool's schema
// states: "an exact span of text, character for character", with nothing said
// about lines.
//
// aider's matcher is line-oriented because a SEARCH/REPLACE block is, and for
// a while the tool inherited that silently — a model fixing a typo mid-line was
// told the text was not in a file that plainly contained it. Found live with
// MiMo-V2.5, which lost three edits of four that way.
func TestDoReplaceMatchesASubstring(t *testing.T) {
	fence := Fence{Open: "```", Close: "```"}
	const content = "Version 3B kept the script but asked the agent to add two seconds\n" +
		"to every shot with dialogue as a crude fix for dialog clipping.\n"

	got, how, ok := DoReplace("notes.md", content, true, "dialog clipping", "dialogue clipping", fence)
	if !ok {
		t.Fatal("a substring inside a line must match")
	}
	if how != MatchExact {
		t.Errorf("match = %v, want exact: the text occurred verbatim", how)
	}
	if !strings.Contains(got, "for dialogue clipping.") || strings.Contains(got, "for dialog clipping.") {
		t.Errorf("substring not replaced:\n%q", got)
	}
	// The rest of the line, and the rest of the file, are untouched.
	if !strings.Contains(got, "to every shot with dialogue as a crude fix") {
		t.Errorf("the surrounding line was disturbed:\n%q", got)
	}
}

// TestDoReplaceDeclinesAnAmbiguousSubstring: replacing the first of several
// matches is a coin flip on the model's behalf. Declining is the stricter
// choice, and CountOccurrences lets the caller say why.
func TestDoReplaceDeclinesAnAmbiguousSubstring(t *testing.T) {
	fence := Fence{Open: "```", Close: "```"}
	const content = "alpha here\nbeta there\nalpha again\n"

	if n := CountOccurrences(content, "alpha"); n != 2 {
		t.Errorf("CountOccurrences = %d, want 2", n)
	}
	if got, how, ok := DoReplace("f.txt", content, true, "alpha", "ALPHA", fence); ok {
		t.Errorf("an ambiguous substring must not be replaced, got %q (%v)", got, how)
	}
}

// A whole-line search still works, and still goes through the fuzzy matcher
// when whitespace drifted — the substring path is an addition, not a
// replacement.
func TestDoReplaceStillMatchesWholeLines(t *testing.T) {
	fence := Fence{Open: "```", Close: "```"}
	const content = "func main() {\n    println(\"hi\")\n}\n"

	got, how, ok := DoReplace("m.go", content, true, "    println(\"hi\")\n", "    println(\"bye\")\n", fence)
	if !ok {
		t.Fatal("a whole-line search must still match")
	}
	if how != MatchExact {
		t.Errorf("match = %v, want exact: this line does occur verbatim", how)
	}
	if !strings.Contains(got, "println(\"bye\")") {
		t.Errorf("line not replaced:\n%q", got)
	}
}

// TestDoReplaceReportsTheFuzzyTier is what the Match return exists for: the
// caller must be able to tell an edit the model located itself from one the
// line matcher located on its behalf. Here the search text has the wrong
// indentation — four spaces where the file has a tab — so it occurs nowhere
// verbatim and only the line matcher can place it.
func TestDoReplaceReportsTheFuzzyTier(t *testing.T) {
	fence := Fence{Open: "```", Close: "```"}
	const content = "func main() {\n\tprintln(\"hi\")\n}\n"

	got, how, ok := DoReplace("m.go", content, true,
		"    println(\"hi\")\n", "    println(\"bye\")\n", fence)
	if !ok {
		t.Fatal("the whitespace-tolerant matcher must still place this line")
	}
	if how != MatchLines {
		t.Errorf("match = %v, want lines: the search text occurs nowhere verbatim", how)
	}
	if !strings.Contains(got, "println(\"bye\")") {
		t.Errorf("line not replaced:\n%q", got)
	}
	// And the file's own indentation survives the harness's guess.
	if !strings.Contains(got, "\tprintln(\"bye\")") {
		t.Errorf("the file's tab indentation was not preserved:\n%q", got)
	}
}

// TestFindSimilarLinesSeesPastIndentation is the CatchUp regression
// (2026-09-10). A model rewrote a bracket-balance block from memory: it
// dropped the enclosing `case` line and the `if c == …` guard, and put the
// rest one tab too shallow. Thirteen of its fourteen lines were in the file
// modulo indentation and three were there verbatim, so a verbatim comparison
// scored 0.143 against a threshold of 0.6 and the did-you-mean stayed silent —
// leaving the model to spend a `read` on what the failure could have told it.
//
// The block below is that region. The counter-arm is the point: the same
// search against a file that does not contain the phenomenon must stay silent,
// or this is measuring nothing.
func TestFindSimilarLinesSeesPastIndentation(t *testing.T) {
	const content = "package web\n" +
		"\n" +
		"func trimTrailingPunct(url string) string {\n" +
		"\tfor url != \"\" {\n" +
		"\t\tc := url[len(url)-1]\n" +
		"\t\tswitch c {\n" +
		"\t\tcase '.', ',', ')', ']', '}':\n" +
		"\t\t\tif c == ')' || c == ']' || c == '}' {\n" +
		"\t\t\t\topen := byte('(')\n" +
		"\t\t\t\tswitch c {\n" +
		"\t\t\t\tcase ']':\n" +
		"\t\t\t\t\topen = '['\n" +
		"\t\t\t\tcase '}':\n" +
		"\t\t\t\t\topen = '{'\n" +
		"\t\t\t\t}\n" +
		"\t\t\t\tif strings.Count(url, string(open)) < 1 {\n" +
		"\t\t\t\t\treturn url\n" +
		"\t\t\t\t}\n" +
		"\t\t\t}\n" +
		"\t\t\turl = url[:len(url)-1]\n" +
		"\t\tdefault:\n" +
		"\t\t\treturn url\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn url\n" +
		"}\n"

	// One tab shallower than the file, and missing the two lines that wrap it.
	const search = "\t\tcase ')', ']', '}':\n" +
		"\t\t\topen := byte('(')\n" +
		"\t\t\tswitch c {\n" +
		"\t\t\tcase ']':\n" +
		"\t\t\t\topen = '['\n" +
		"\t\t\tcase '}':\n" +
		"\t\t\t\topen = '{'\n" +
		"\t\t\t}\n" +
		"\t\t\tif strings.Count(url, string(open)) < 1 {\n" +
		"\t\t\t\treturn url\n" +
		"\t\t\t}\n" +
		"\t\t\turl = url[:len(url)-1]\n"

	got := FindSimilarLines(search, content, 0.6)
	if got == "" {
		t.Fatal("did-you-mean stayed silent on a block that differs only by indentation and two wrapper lines")
	}
	// It has to show the two lines the model did not know were there; those
	// are the whole reason the edit failed.
	for _, want := range []string{"case '.', ',', ')', ']', '}':", "if c == ')' || c == ']' || c == '}' {"} {
		if !strings.Contains(got, want) {
			t.Errorf("did-you-mean omits the line the model was missing: %q\ngot:\n%s", want, got)
		}
	}
	// The file's real indentation is what the model needs to copy, so it must
	// survive into the suggestion rather than being normalized away with it.
	if !strings.Contains(got, "\t\t\t\topen := byte('(')") {
		t.Errorf("suggestion lost the file's indentation:\n%s", got)
	}

	// Counter-arm: a file that cannot contain the phenomenon.
	const other = "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if s := FindSimilarLines(search, other, 0.6); s != "" {
		t.Errorf("fired on a file with no such block:\n%s", s)
	}
}

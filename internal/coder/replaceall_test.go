package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// The field report's file, reduced: the same version string in two matrix
// entries, both wanting the same replacement.
const matrixFile = `        os:
          - name: freebsd
            architecture: aarch64
            version: '14.3'

          - name: freebsd
            architecture: x86-64
            version: '14.3'
`

// editToolCall builds a raw edit tool call, so these go through
// parseEditArgs the way a model's call does rather than around it.
func editToolCall(id, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: toolEdit, Arguments: args}
}

func TestReplaceAllChangesEveryOccurrence(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	path := filepath.Join(dir, "ci.yml")
	if err := os.WriteFile(path, []byte(matrixFile), 0o644); err != nil {
		t.Fatal(err)
	}

	results := toolResults{}
	matchFailure := false
	e, msg := parseEditArgs(editToolCall("c1",
		`{"path":"ci.yml","old_string":"version: '14.3'","new_string":"version: '14.5'","replace_all":true}`))
	if msg != "" {
		t.Fatal(msg)
	}
	c.applyToolEdits([]plannedEdit{e}, results, &matchFailure)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "14.3") {
		t.Errorf("an occurrence survived:\n%s", got)
	}
	if n := strings.Count(string(got), "14.5"); n != 2 {
		t.Errorf("replaced %d occurrences, want 2:\n%s", n, got)
	}
	// The count has to come back: a model that asked for "all of them" has no
	// other way to learn whether that was two or twenty.
	if !strings.Contains(results["c1"].Text, "2 occurrences") {
		t.Errorf("the result does not say how many changed: %q", results["c1"].Text)
	}
	if matchFailure {
		t.Error("a successful replace_all asked for a reflection")
	}
}

// Without it, ambiguity is still a failure. This is the behaviour replace_all
// is opting out of, so it has to keep working.
func TestWithoutReplaceAllAmbiguityStillFails(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	path := filepath.Join(dir, "ci.yml")
	if err := os.WriteFile(path, []byte(matrixFile), 0o644); err != nil {
		t.Fatal(err)
	}

	results := toolResults{}
	matchFailure := false
	e, _ := parseEditArgs(editToolCall("c1",
		`{"path":"ci.yml","old_string":"version: '14.3'","new_string":"version: '14.5'"}`))
	c.applyToolEdits([]plannedEdit{e}, results, &matchFailure)

	if got, _ := os.ReadFile(path); strings.Contains(string(got), "14.5") {
		t.Error("an ambiguous edit was applied without replace_all")
	}
	msg := results["c1"].Text
	if !strings.Contains(msg, "appears 2 times") {
		t.Errorf("no ambiguity message: %q", msg)
	}
	// Named at the moment of need. Our own trials put schema-only uptake at
	// 1/18 and 0/36 -- "both unused when unnamed" -- and naming the feature
	// where it is wanted moved the comparable case from 0/24 to 8/24.
	if !strings.Contains(msg, "replace_all") {
		t.Errorf("the ambiguity message does not name replace_all:\n%s", msg)
	}
	// And both branches, the way Kimi Code's message does.
	if !strings.Contains(msg, "To change one of them") {
		t.Errorf("the message lost the disambiguate-one branch:\n%s", msg)
	}
}

// Exact matches only. DoReplace tolerates whitespace drift at one site, which
// is a good bet once and a bad one everywhere at once.
func TestReplaceAllDoesNotFuzzyMatch(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("    alpha\n    alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results := toolResults{}
	matchFailure := false
	// Wrong indentation: not present verbatim.
	e, _ := parseEditArgs(editToolCall("c1",
		`{"path":"a.txt","old_string":"alpha","new_string":"beta","replace_all":true}`))
	_ = e
	e2, _ := parseEditArgs(editToolCall("c2",
		`{"path":"a.txt","old_string":"        alpha","new_string":"        beta","replace_all":true}`))
	c.applyToolEdits([]plannedEdit{e2}, results, &matchFailure)

	if got, _ := os.ReadFile(path); strings.Contains(string(got), "beta") {
		t.Errorf("a non-verbatim replace_all was applied:\n%s", got)
	}
}

// An anchor already names one range by identity, so "all of them" cannot mean
// anything; saying so beats silently honouring one of the two.
func TestReplaceAllWithAnchorIsRefused(t *testing.T) {
	_, msg := parseEditArgs(editToolCall("c1",
		`{"path":"a.txt","anchor":"a1","new_string":"x","replace_all":true}`))
	if msg == "" {
		t.Fatal("anchor plus replace_all was accepted")
	}
	if !strings.Contains(msg, "anchor") || !strings.Contains(msg, "replace_all") {
		t.Errorf("the refusal does not name both: %q", msg)
	}
}

// Default off, which the whole panel agrees on and our own trial cannot
// contradict, since its hazard never fired.
func TestReplaceAllDefaultsOff(t *testing.T) {
	e, msg := parseEditArgs(editToolCall("c1", `{"path":"a.txt","old_string":"x","new_string":"y"}`))
	if msg != "" {
		t.Fatal(msg)
	}
	if e.replaceAll {
		t.Error("replace_all defaulted to true")
	}
}

// An old_string whose matches overlap names two places, and the edit used to
// take the first and report "Applied the edit" — the uniqueness check counted
// copies with strings.Count, which finds one "}\n}\n" in "}\n}\n}\n". It is
// refused as ambiguous now, and replace_all over the same file reports the
// count it actually replaced, which is ReplaceAll's non-overlapping one.
func TestOverlappingMatchesAreAmbiguous(t *testing.T) {
	const file = "x\n}\n}\n}\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	c.AddFile("a.txt")

	results := toolResults{}
	matchFailure := false
	edited := c.applyToolEdits([]plannedEdit{
		{callID: "c1", path: "a.txt", search: "}\n}\n", replace: "}\n// end\n}\n"},
	}, results, &matchFailure)
	if len(edited) != 0 {
		t.Errorf("edited = %v; two overlapping matches are two places the model could mean", edited)
	}
	if got, _ := os.ReadFile(path); string(got) != file {
		t.Errorf("file = %q, want it untouched", got)
	}
	if got := results["c1"].Text; !strings.Contains(got, "appears 2 times") {
		t.Errorf("result = %q, want the ambiguity named with its count", got)
	}

	results = toolResults{}
	c.applyToolEdits([]plannedEdit{
		{callID: "c2", path: "a.txt", search: "}\n}\n", replace: "]\n]\n", replaceAll: true},
	}, results, &matchFailure)
	if got, _ := os.ReadFile(path); string(got) != "x\n]\n]\n}\n" {
		t.Errorf("replace_all file = %q", got)
	}
	if got := results["c2"].Text; !strings.Contains(got, "Replaced 1 occurrence") {
		t.Errorf("result = %q, want the one replacement ReplaceAll made", got)
	}
}

package coder

import (
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/prompts"
)

// No assembled prompt fragment may name a tool the schema withholds. This is
// the rule CodeToolsBullet's comment states and the rule TestCodeToolsSlot-
// TracksTheSchema was written for -- but that test greps only MainSystem for
// the bullet's own phrase, so it passed while the nothing-pinned note went on
// offering "one run_code call" to a session with no run_code in its schema.
//
// Checking every fragment against the tool set, rather than one fragment
// against one phrase, is what makes this catch the next one too.
func TestNoPromptFragmentNamesAWithheldTool(t *testing.T) {
	for _, format := range []string{"tool", "ask"} {
		c := testCoder(t)
		c.editFormat = format
		c.OfferCode = false
		c.ObservationViaRunCode = false

		var offered []string
		for _, d := range c.toolDefs() {
			offered = append(offered, d.Name)
		}
		if slices.Contains(offered, "run_code") {
			t.Fatalf("%s: run_code is in the schema; this test assumes it is withheld", format)
		}

		for name, text := range map[string]string{
			"MainSystem":       c.fmtSystemPrompt(c.Prompts.MainSystem),
			"FilesNoFullFiles": c.filesNoFullFilesText(),
			"SystemReminder":   c.Prompts.SystemReminder,
		} {
			if strings.Contains(text, "run_code") {
				t.Errorf("%s/%s names run_code while the schema withholds it:\n  %s",
					format, name, text)
			}
		}
	}
}

// And the offered direction, so the gate cannot be "fixed" by never offering
// the clause at all.
func TestNothingPinnedNoteOffersRunCodeWhenItIsOffered(t *testing.T) {
	c := testCoder(t)
	c.OfferCode = true
	c.ObservationViaRunCode = false
	if !strings.Contains(c.filesNoFullFilesText(), "run_code") {
		t.Error("run_code is offered, but the nothing-pinned note does not mention it")
	}
}

// The splice must not have changed the wording 2026-09-code-mode2.md measured.
// Both variants are built from shared parts precisely so they cannot drift, and
// this pins the composed result against the text as it shipped for that trial.
func TestNothingPinnedNoteIsByteIdenticalToTheTrialsWording(t *testing.T) {
	const asMeasured = "Nothing is pinned for editing. Use read, grep, glob, and ls to find " +
		"what you need — or one run_code call that calls them, when you already " +
		"know the several things you are looking for. You can edit any file in " +
		"the project. If the user asks how something here works, read the code " +
		"that implements it: what you remember about a project is not evidence " +
		"about this one."
	if prompts.Tool.FilesNoFullFiles != asMeasured {
		t.Errorf("the offered note changed wording:\n got %q\nwant %q",
			prompts.Tool.FilesNoFullFiles, asMeasured)
	}
	// The withheld variant is the same sentence with the clause taken out.
	want := strings.Replace(asMeasured,
		" — or one run_code call that calls them, when you already "+
			"know the several things you are looking for", "", 1)
	if prompts.Tool.FilesNoFullFilesNoCode != want {
		t.Errorf("the withheld note is not the offered one minus the clause:\n got %q\nwant %q",
			prompts.Tool.FilesNoFullFilesNoCode, want)
	}
	if strings.Contains(prompts.Ask.FilesNoFullFilesNoCode, "run_code") {
		t.Error("the ask set's withheld note still names run_code")
	}
}

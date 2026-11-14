package prompts

import (
	"strings"
	"testing"
)

func TestSystemPromptShape(t *testing.T) {
	// System is literal: nothing substitutes into it, so a brace in it is
	// either prose or a slot left over from the template engine, and the
	// prose has none.
	if strings.ContainsAny(System, "{}") {
		t.Errorf("System has a brace, which nothing will fill: %q", System)
	}

	// The tool groups and their costs are the part the schema cannot carry.
	for _, want := range []string{
		"run_code runs a short JavaScript program",
		"change nothing and need no permission", // read/grep/glob/ls
		"change files directly",                 // edit/write
		"the user is asked before it runs",      // bash
	} {
		if !strings.Contains(System, want) {
			t.Errorf("System should convey %q", want)
		}
	}
	// The loop's shape is the other thing only the prompt can say: results come
	// back, and a reply without a tool call is what ends the turn.
	if !strings.Contains(System, "result comes back to you") {
		t.Error("System should tell the model its tool results return")
	}
	if !strings.Contains(System, "ends the turn") {
		t.Error("System should say what ends the turn")
	}
	// The reach clause is measured, not stylistic: without it three models
	// updated a stale test 76 times in 90, with it 87 (CMH p=0.011). It is
	// pinned so a tidy-up cannot quietly revert an experiment.
	// See doc/experiments/2026-08-prompt-scope/README.md.
	for _, want := range []string{
		"Carry the change through everywhere it reaches",
		"the tests that cover it",
		"That is the same request, not extra work",
	} {
		if !strings.Contains(System, want) {
			t.Errorf("the scope block should say %q", want)
		}
	}
	// The bans survive alongside it: the clause buys reach, and the block still
	// has to forbid the drive-by work it was inherited to forbid.
	if !strings.Contains(System, "no drive-by refactoring") {
		t.Error("the scope block lost its ban list")
	}
	if !strings.Contains(System, "# Editing rules") {
		t.Error("System lost the editing rules")
	}
}

// The prompt says what commit does and not when to use it: whether a model
// commits unasked is left to watch. Pinned so that an instruction does not
// creep back in through a tidy-up.
func TestSystemPromptDoesNotSteerCommits(t *testing.T) {
	if !strings.Contains(System, commitBullet) {
		t.Error("System lost the commit bullet")
	}
	for _, steer := range []string{"commit it with", "call commit", "commit the fix", "needs no call"} {
		if strings.Contains(System, steer) {
			t.Errorf("System steers commits: %q", steer)
		}
	}
}

// The how-to-look note is the measured text of 2026-09-code-mode2.md, run_code
// clause included; it is pinned whole so an edit to it is a decision.
func TestHowToLookIsAsMeasured(t *testing.T) {
	const asMeasured = "Use read, grep, glob, and ls to find what you need — or one run_code " +
		"call that calls them, when you already know the several things you are looking for. " +
		"You can edit any file in the project. If the user asks how something here works, read " +
		"the code that implements it: what you remember about a project is not evidence about " +
		"this one."
	if !strings.HasSuffix(System, asMeasured) {
		t.Errorf("System does not end with the measured how-to-look note:\n%s", System)
	}
}

// The harness speaks to the model in marked user-role messages, and the prompt
// has to say what the mark means: without it, models read an unmarked note as
// the user's words (doc/experiments/2026-09-note-attribution).
func TestPromptDefinesTheHarnessMarker(t *testing.T) {
	if !strings.Contains(System, HarnessNoteLine) {
		t.Error("System does not define the [strument] marker")
	}
}

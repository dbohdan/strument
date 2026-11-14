package prompts

import (
	"strings"
	"testing"
)

func TestToolPromptShape(t *testing.T) {
	// The tool format's system prompt uses only the {final_reminders},
	// {platform}, {code_tools}, and {observation_tools} slots (the schema does
	// the rest); no other braces may linger to survive substitution as a
	// literal.
	for _, slot := range []string{"{final_reminders}", "{platform}", "{code_tools}", "{observation_tools}"} {
		if !strings.Contains(Tool.MainSystem, slot) {
			t.Errorf("tool main_system missing slot %s", slot)
		}
	}
	stripped := Tool.MainSystem
	for _, slot := range []string{"{final_reminders}", "{platform}", "{code_tools}", "{observation_tools}"} {
		stripped = strings.ReplaceAll(stripped, slot, "")
	}
	if strings.ContainsAny(stripped, "{}") {
		t.Errorf("tool main_system has an unexpected brace: %q", stripped)
	}
	if !strings.Contains(Tool.SystemReminder, "{final_reminders}") {
		t.Errorf("tool system_reminder missing {final_reminders}")
	}

	// The observation paragraph moved behind {observation_tools}, which the
	// coder fills from the tool-set flag; the pinned phrases live in
	// ObservationBullet now. The other two group separations are still in the
	// template itself — that is the part the schema cannot carry.
	for _, want := range []string{
		"change files directly",            // edit/write
		"the user is asked before it runs", // bash
	} {
		if !strings.Contains(Tool.MainSystem, want) {
			t.Errorf("tool main_system should convey %q", want)
		}
	}
	if obs := ObservationBullet; !strings.Contains(obs, "change nothing and need no permission") {
		t.Errorf("the observation bullet lost its cost sentence: %q", obs)
	}
	if !strings.Contains(Tool.MainSystem, "{observation_tools}") {
		t.Error("tool main_system lost the observation slot")
	}
	// The loop's shape is the other thing only the prompt can say: results come
	// back, and a reply without a tool call is what ends the turn.
	if !strings.Contains(Tool.MainSystem, "result comes back to you") {
		t.Error("tool main_system should tell the model its tool results return")
	}
	if !strings.Contains(Tool.MainSystem, "ends the turn") {
		t.Error("tool main_system should say what ends the turn")
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
		if !strings.Contains(Tool.MainSystem, want) {
			t.Errorf("the scope block should say %q", want)
		}
	}
	// The bans survive alongside it: the clause buys reach, and the block still
	// has to forbid the drive-by work it was inherited to forbid.
	if !strings.Contains(Tool.MainSystem, "no drive-by refactoring") {
		t.Error("the scope block lost its ban list")
	}
}

// The harness speaks to the model in marked user-role messages, and the prompt
// has to say what the mark means: without it, models read an unmarked note as
// the user's words (doc/experiments/2026-09-note-attribution).
func TestPromptDefinesTheHarnessMarker(t *testing.T) {
	if !strings.Contains(Tool.MainSystem, HarnessNoteLine) {
		t.Error("main_system does not define the [strument] marker")
	}
}

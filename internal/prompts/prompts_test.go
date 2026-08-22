package prompts

import (
	"strings"
	"testing"
)

func TestToolPromptShape(t *testing.T) {
	// The tool format's system prompt uses only the {final_reminders} and
	// {platform} slots (the schema does the rest); no other braces may linger
	// to survive substitution as a literal.
	for _, slot := range []string{"{final_reminders}", "{platform}"} {
		if !strings.Contains(Tool.MainSystem, slot) {
			t.Errorf("tool main_system missing slot %s", slot)
		}
	}
	stripped := Tool.MainSystem
	for _, slot := range []string{"{final_reminders}", "{platform}"} {
		stripped = strings.ReplaceAll(stripped, slot, "")
	}
	if strings.ContainsAny(stripped, "{}") {
		t.Errorf("tool main_system has an unexpected brace: %q", stripped)
	}
	if !strings.Contains(Tool.SystemReminder, "{final_reminders}") {
		t.Errorf("tool system_reminder missing {final_reminders}")
	}

	// The prompt must convey what separates the three groups of tools, since
	// that is the part the schema cannot carry: observation is free, edits land
	// directly, and the shell asks first.
	for _, want := range []string{
		"change nothing and need no permission", // read/grep/glob/ls
		"change files directly",                 // edit/write
		"the user is asked before it runs",      // bash
	} {
		if !strings.Contains(Tool.MainSystem, want) {
			t.Errorf("tool main_system should convey %q", want)
		}
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
	// See doc/experiments/2026-08-prompt-scope.md.
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

	// The schema carries the format, so no few-shot examples are needed.
	if len(Tool.ExampleMessages) != 0 {
		t.Errorf("tool example_messages should be empty, got %d", len(Tool.ExampleMessages))
	}
}

func TestAskPromptShape(t *testing.T) {
	// Ask has no few-shot examples: the examples chunk is absent, so the
	// cache-placement breakpoint falls back to system (assembly test).
	if len(Ask.ExampleMessages) != 0 {
		t.Errorf("ask example_messages must be empty, got %d", len(Ask.ExampleMessages))
	}
	// system_reminder is not empty, so the reminder gate still runs. It used to
	// say "overeager rides in through {final_reminders}", which was never true:
	// finalReminders holds the language line and nothing else, and Ask's
	// OvereagerPrompt field was read by no code in the tree. A reviewer grepped
	// for the splice, found none, and reported that the whole block — "Do not
	// return fully detailed code or full diffs" — had never reached a model.
	// The text lives in MainSystem now and the fields are gone.
	if Ask.SystemReminder != "{final_reminders}" {
		t.Errorf("ask system_reminder = %q, want {final_reminders}", Ask.SystemReminder)
	}
	// The falsy sentinel: empty string disables the repo-map branch (it is
	// NOT "emit an empty pair"). assemble.go must treat "" as "skip".
	if Ask.FilesNoFullFilesWithRepoMap != "" {
		t.Errorf("ask files_no_full_files_with_repo_map must be the empty sentinel, got %q", Ask.FilesNoFullFilesWithRepoMap)
	}
	// Ask must describe the tools it actually has. It offers read, grep, glob,
	// ls, and symbol; it once named none of them and opened by saying what it
	// could not do, which left a model with no picture of the mode and no reason
	// to look at anything. These are the sentences whose absence caused that, so
	// they are pinned rather than left to a future tidy-up.
	for _, want := range []string{
		"read, grep, glob, and ls look at the project",
		"change nothing and need no permission",
		"result comes back to you", // the loop closes here too
		"ends the turn",
	} {
		if !strings.Contains(Ask.MainSystem, want) {
			t.Errorf("ask main_system should convey %q:\n%s", want, Ask.MainSystem)
		}
	}
	// The no-editing rule has to name the mechanism rather than sound like a ban
	// on acting: this mode has no editing tools, so describe changes instead.
	if !strings.Contains(Ask.MainSystem, "no editing tools") {
		t.Errorf("ask main_system should say why it cannot edit:\n%s", Ask.MainSystem)
	}
	if strings.Contains(Ask.MainSystem, "cannot apply edits from it") {
		t.Error("ask main_system reverted to the phrasing that read as 'you cannot act'")
	}
	// The brake on a full rewrite. It sat in the unread OvereagerPrompt field
	// for the whole life of the tool format, so ask mode's only limit on output
	// was "say briefly", and two reviewers independently said they would be
	// tempted to paste a finished file. Pinned in the string that ships.
	if !strings.Contains(Ask.MainSystem, "full diff") {
		t.Errorf("ask main_system does not rule out returning a full diff:\n%s", Ask.MainSystem)
	}
	// Nothing is pinned for editing here because nothing can be: the flat
	// denial "No files are pinned to this session" co-occurs with a read-only
	// reference block whose contents are in the next message, which is
	// 2026-08-readonly-honest.md's twelve-step file hunt.
	if strings.Contains(Ask.FilesNoFullFiles, "No files are pinned") {
		t.Errorf("the ask empty-pin line is a flat denial again:\n%s", Ask.FilesNoFullFiles)
	}
	// {language} is the only slot Ask substitutes; a stray brace would survive
	// into the prompt as a literal.
	if strings.ContainsAny(strings.ReplaceAll(Ask.MainSystem, "{language}", ""), "{}") {
		t.Errorf("ask main_system has an unexpected brace:\n%s", Ask.MainSystem)
	}
}

// TestCommitSystemScopesToTheDiff pins the clause that makes the widened
// commit context safe. Shown earlier turns, models described the *previous*
// turn's change on this commit — with a BREAKING CHANGE marker for a break the
// diff did not contain.
func TestCommitSystemScopesToTheDiff(t *testing.T) {
	if !strings.Contains(CommitSystem, "Earlier turns are background") {
		t.Error("the commit prompt does not scope the message to the diff")
	}
	if !strings.Contains(CommitSystem, "not part of this commit") {
		t.Error("the prompt permits earlier work to be described as this change")
	}
}

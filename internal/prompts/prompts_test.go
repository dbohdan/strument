package prompts

import (
	"strings"
	"testing"
)

func TestPromptSlotsPresent(t *testing.T) {
	for _, slot := range []string{"{final_reminders}", "{shell_cmd_prompt}"} {
		if !strings.Contains(EditBlock.MainSystem, slot) {
			t.Errorf("main_system missing slot %s", slot)
		}
	}
	for _, slot := range []string{"{fence[0]}", "{quad_backtick_reminder}", "{rename_with_shell}", "{go_ahead_tip}", "{shell_cmd_reminder}"} {
		if !strings.Contains(EditBlock.SystemReminder, slot) {
			t.Errorf("system_reminder missing slot %s", slot)
		}
	}
	// The leaked merge-conflict marker upstream left at the
	// end of the diff-fenced example (editblock_fenced_prompts.py @ 5dc9490)
	// is dropped. It must not reappear.
	if strings.Contains(EditBlockFenced.ExampleMessages[1].Content, "<<<<<<< HEAD") {
		t.Error("fenced example[1] still carries the leaked '<<<<<<< HEAD' marker")
	}
	if WholeFile.RedactedEditMessage != "No changes are needed." {
		t.Errorf("redacted_edit_message = %q", WholeFile.RedactedEditMessage)
	}
}

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

	// The prompt must convey the tools' differing natures: edits are direct,
	// commands are suggestions the user approves.
	if !strings.Contains(Tool.MainSystem, "directly") {
		t.Error("tool main_system should state that edits apply directly")
	}
	if !strings.Contains(Tool.MainSystem, "does not run until the user approves") {
		t.Error("tool main_system should state that suggested commands need approval")
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
	// system_reminder is not empty — the reminder gate still runs and
	// overeager rides in through {final_reminders}.
	if Ask.SystemReminder != "{final_reminders}" {
		t.Errorf("ask system_reminder = %q, want {final_reminders}", Ask.SystemReminder)
	}
	// The falsy sentinel: empty string disables the repo-map branch (it is
	// NOT "emit an empty pair"). assemble.go must treat "" as "skip".
	if Ask.FilesNoFullFilesWithRepoMap != "" {
		t.Errorf("ask files_no_full_files_with_repo_map must be the empty sentinel, got %q", Ask.FilesNoFullFilesWithRepoMap)
	}
	// Ask's repo_content_prefix invites the add-file flow rather than
	// forbidding edits.
	if !strings.Contains(Ask.RepoContentPrefix, "add them to the chat") {
		t.Errorf("ask repo_content_prefix should invite adding files: %q", Ask.RepoContentPrefix)
	}
}

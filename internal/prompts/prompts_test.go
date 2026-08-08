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

package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func h16(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// Hashes pinned against the mechanical dump of aider @ 5dc9490 prompt
// classes (2026-07-16). A mismatch means someone edited an [Exact] string.
func TestPromptParityHashes(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"editblock main_system", EditBlock.MainSystem, "094e6413f2662f59"},
		{"editblock system_reminder", EditBlock.SystemReminder, "c4dcb982dabef440"},
		{"editblock example[1]", EditBlock.ExampleMessages[1].Content, "d4d39fbabea3c429"},
		{"fenced system_reminder", EditBlockFenced.SystemReminder, "3272161841f387f4"},
		// Fenced example[1] with the leaked "<<<<<<< HEAD" marker removed
		// (Deviation D5). Pins the corrected string.
		{"fenced example[1]", EditBlockFenced.ExampleMessages[1].Content, "4e08bacc735dd5fe"},
		{"wholefile main_system", WholeFile.MainSystem, "8b75d2534efc3659"},
		{"ask main_system", Ask.MainSystem, "2687bda9297ee8f5"},
		{"ask overeager_prompt", Ask.OvereagerPrompt, "35adeb853163a502"},
		{"ask repo_content_prefix", Ask.RepoContentPrefix, "9689be74e9e5bd26"},
	}
	for _, c := range cases {
		if got := h16(c.got); got != c.want {
			t.Errorf("%s: hash %s, want %s", c.name, got, c.want)
		}
	}
	if len(EditBlock.MainSystem) != 1056 || len(EditBlock.SystemReminder) != 2165 {
		t.Errorf("lengths drifted: %d, %d", len(EditBlock.MainSystem), len(EditBlock.SystemReminder))
	}
}

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
	// Deviation D5: the leaked merge-conflict marker upstream left at the
	// end of the diff-fenced example (editblock_fenced_prompts.py @ 5dc9490)
	// is dropped. It must not reappear.
	if strings.Contains(EditBlockFenced.ExampleMessages[1].Content, "<<<<<<< HEAD") {
		t.Error("fenced example[1] still carries the leaked '<<<<<<< HEAD' marker (Deviation D5)")
	}
	if WholeFile.RedactedEditMessage != "No changes are needed." {
		t.Errorf("redacted_edit_message = %q", WholeFile.RedactedEditMessage)
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

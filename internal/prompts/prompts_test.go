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
		{"wholefile main_system", WholeFile.MainSystem, "8b75d2534efc3659"},
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
	// The upstream wart carried verbatim: a leaked merge-conflict marker in
	// the fenced example (editblock_fenced_prompts.py @ 5dc9490).
	if !strings.Contains(EditBlockFenced.ExampleMessages[1].Content, "<<<<<<< HEAD") {
		t.Error("fenced example[1] lost the verbatim '<<<<<<< HEAD' wart; parity broken")
	}
	if WholeFile.RedactedEditMessage != "No changes are needed." {
		t.Errorf("redacted_edit_message = %q", WholeFile.RedactedEditMessage)
	}
}

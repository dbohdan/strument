package coder

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// The definition in the system prompt names the marker the notes carry. Two
// spellings in two packages would drift apart silently.
func TestHarnessNoteLineNamesTheMarker(t *testing.T) {
	if !strings.Contains(prompts.HarnessNoteLine, llm.HarnessMarker) {
		t.Errorf("prompts.HarnessNoteLine %q does not name llm.HarnessMarker %q",
			prompts.HarnessNoteLine, llm.HarnessMarker)
	}
}

// unattendedConfirmer declines every prompt the way script mode does when no
// one is at a terminal.
type unattendedConfirmer struct{}

func (unattendedConfirmer) Confirm(ConfirmRequest) ConfirmResult {
	return ConfirmResult{Unattended: true}
}

// A declined command tells the model who declined it. "The user chose not to"
// is true only when a user saw the question; in script mode with no terminal
// nobody did, and a model reading that sentence told the user "you've
// declined them twice".
func TestDeclinedCommandsSayWhoDeclined(t *testing.T) {
	for _, tc := range []struct {
		name      string
		confirmer Confirmer
		want, not string
	}{
		{"a person said no", noConfirmer{}, "The user chose not to run the command.", "no one"},
		{"no one could be asked", unattendedConfirmer{}, "no one is at a terminal", "The user chose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCoder(t)
			c.Confirm = tc.confirmer
			c.SuggestShellCommands = true
			got, ran := c.runShell(context.Background(), toolCommand{command: "echo hi", purpose: "test"})
			if ran {
				t.Fatal("a declined command ran")
			}
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) {
				t.Errorf("got %q, want it to contain %q and not %q", got, tc.want, tc.not)
			}
			if tc.confirmer == (unattendedConfirmer{}) && !strings.Contains(got, "--yes bash") {
				t.Errorf("an unattended decline should name the --yes that would approve it: %q", got)
			}
		})
	}
}

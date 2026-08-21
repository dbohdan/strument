// Who is on record as having said what.

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// No path by which the harness adds to the conversation may write an assistant
// turn.
//
// The assistant role is the model's alone. Everything the harness needs to say
// mid-conversation — an interruption, an /undo, output the user chose to add —
// goes in a marked user turn, and the system role stays in the prefix where
// every provider accepts it.
//
// Four sites used to break this, three of them with the word "Ok." The reason
// they existed was role alternation, and nothing requires it: two consecutive
// user messages are accepted everywhere, which is the shape this leaves behind.
// The one thing no provider will catch for us is a reply the model never gave,
// so it is checked here.
func TestTheHarnessNeverSpeaksAsTheAssistant(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(c *Coder)
	}{
		{"interrupt", func(c *Coder) { c.noteInterrupt() }},
		{"undo", func(c *Coder) { c.NoteUndo([]string{"a.go"}) }},
		{"/run or /web output", func(c *Coder) { c.AppendContext("Command: ls\nOutput:\na.go") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCoder(t)
			// A conversation the model has already spoken in, so a fabricated
			// reply would have something real to hide among.
			said := "I have finished the edit."
			c.curMessages = []llm.Message{
				llm.TextMessage(llm.RoleUser, "do the thing"),
				llm.TextMessage(llm.RoleAssistant, said),
			}
			c.doneMessages = nil

			tc.act(c)

			for _, m := range append(append([]llm.Message{}, c.doneMessages...), c.curMessages...) {
				if m.Role != llm.RoleAssistant {
					continue
				}
				if m.Text() != said {
					t.Errorf("the harness wrote an assistant turn: %q", m.Text())
				}
			}
		})
	}
}

// Anything the harness says in the conversation says so.
//
// Without the marker a note is indistinguishable from the user's own words, to
// the model and to anyone reading the transcript later. The marker is the whole
// reason a user-role message is an acceptable carrier for the harness's voice
// after Anthropic ruled out the system role in this position.
func TestHarnessNotesAreMarked(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{llm.TextMessage(llm.RoleUser, "go")}
	c.noteInterrupt()
	c.NoteUndo([]string{"a.go"})

	for _, m := range append(append([]llm.Message{}, c.doneMessages...), c.curMessages...) {
		if m.Text() == "go" {
			continue // the user's own words, which carry no marker
		}
		if !strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			t.Errorf("unmarked harness message: %q", m.Text())
		}
	}
}

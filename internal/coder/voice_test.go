// Who is on record as having said what.

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
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

// The compaction summary is a marked harness turn, not a system message.
//
// It was a system message, on the reasoning that the summary is the harness's
// artifact — true, and the wrong conclusion. The system role belongs to the
// prefix; this lands mid-conversation, in done, which is the position
// HarnessNote exists for. It stayed legal only because it is always spliced at
// index 0 of done with nothing assistant-role ahead of it, three facts no test
// pinned. This pins the carrier, and checks the assembled request for the one
// shape a five-provider probe found universally rejected.
//
// Codex CLI and Kimi CLI both use a prefixed user message; OpenCode an
// assistant message flagged as a summary. None uses system.
func TestTheCompactionSummaryIsAMarkedHarnessTurn(t *testing.T) {
	c := testCoder(t)
	c.doneMessages = []llm.Message{
		llm.HarnessNote(prompts.SummaryLabel + "EARLIER WORK"),
		llm.TextMessage(llm.RoleUser, "and then?"),
		llm.TextMessage(llm.RoleAssistant, "I read the file."),
	}
	c.curMessages = []llm.Message{llm.TextMessage(llm.RoleUser, "carry on")}

	summary := c.doneMessages[0]
	if summary.Role == llm.RoleSystem {
		t.Error("the summary is a system message again; it is mid-conversation history")
	}
	if summary.Role == llm.RoleAssistant {
		t.Error("the summary is an assistant turn; the model did not say this")
	}
	if !strings.HasPrefix(summary.Text(), llm.HarnessMarker) {
		t.Errorf("the summary is unmarked, so it reads as the user's own words: %q", summary.Text())
	}

	msgs := c.formatChatChunks().allMessages()
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleSystem && msgs[i-1].Role == llm.RoleAssistant {
			t.Errorf("message %d is a system message following an assistant turn", i)
		}
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

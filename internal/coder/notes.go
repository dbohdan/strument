package coder

import (
	"context"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// Session notes are the durable half of picking a project back up: what the
// work was for, what was decided and why, what was left in flight. They are
// deliberately *not* a replayed conversation.
//
// Every other harness persists a transcript and replays it verbatim — Claude
// Code, Codex CLI, Gemini CLI, OpenCode all do. Three reasons Strument does not.
// Cost: a restored history is re-sent with every message of the next session,
// uncached, and silently until the token line. Attention: everything in the
// window influences the output, so carrying last week's abandoned approach is
// degrading rather than merely wasteful. And attribution: messages labelled
// `assistant` are read by the next model as its own past self, so it will
// rationalize and then defend choices it would never have made, with no seam
// anywhere to notice. A single-vendor harness can lean on the models sharing
// dispositions; Strument is multi-vendor by design and cannot.
//
// Notes are ~300 words instead of tens of thousands of tokens, they go stale
// gracefully where a verbatim history does not, and they assert nothing about
// who said what.

const notesTimeout = 60 * time.Second

// maxNotesInput caps the transcript slice fed to the weak model. The transcript
// grows without bound, and the recent end is what a resumed session needs — the
// older part is either already reflected in the notes being replaced or is
// history the code itself now carries.
const maxNotesInput = 24_000

// NotesWriter returns a function that writes session notes from a project's
// transcript, using the weak model. Same shape as CommitMessenger, and for the
// same reasons: a plain function so the caller owns when it runs, and a record
// hook so the side request reaches the turn's accounting instead of being spent
// invisibly.
//
// It returns "" on any failure. Notes are a convenience; a session that cannot
// write them must still be a session.
func NotesWriter(cl llm.ModelClient, model *config.Model, record func(llm.Usage)) func(transcript string) string {
	return func(transcript string) string {
		transcript = strings.TrimSpace(transcript)
		if transcript == "" {
			return ""
		}
		transcript = sampleTranscript(transcript)

		ctx, cancel := context.WithTimeout(context.Background(), notesTimeout)
		defer cancel()

		var answer strings.Builder
		for ev, err := range cl.Send(ctx, llm.Request{
			Model: model.Slug,
			Messages: []llm.Message{
				llm.TextMessage(llm.RoleSystem, prompts.SessionNotes),
				llm.TextMessage(llm.RoleUser, transcript),
			},
			// No ReasoningEffort, like the commit message: this runs while the
			// user is waiting for their prompt back, and thinking about it is
			// paid for and invisible.
			Temperature: model.Temperature,
			ExtraParams: model.RequestExtraParams(),
		}) {
			if err != nil {
				return ""
			}
			if ev.Kind == llm.EventAnswer {
				answer.WriteString(ev.Text)
			}
			if ev.Kind == llm.EventUsage && ev.Usage != nil && record != nil {
				record(*ev.Usage)
			}
		}
		return strings.TrimSpace(answer.String())
	}
}

// notesHeadShare is how much of the input budget goes to the *oldest* turns.
//
// A tail-only window was the obvious first cut and the wrong one. A session has
// a shape: the opening turns carry the intent and the constraints the user
// stated, the recent turns carry the working state, and the middle is mechanics
// that the code itself now records. Taking only the tail meant that in a long
// session the reason for a decision — the one thing notes exist to preserve, and
// the one thing no diff can give back — fell out of the input while the
// step-by-step of the last hour stayed in.
const notesHeadShare = 4 // one quarter

// sampleTranscript trims a transcript to the input budget, keeping both ends.
//
// The alternative was to feed the previous notes back in alongside the
// transcript, which would also carry the opening forward. It is rejected
// deliberately: regenerating purely from the record is *self-healing*, because a
// confabulated reason has exactly one life and the next regeneration wipes it,
// while folding the previous notes in is *self-reinforcing* — the invention gets
// re-endorsed every cycle and becomes indistinguishable from a real decision.
// The compaction trial produced exactly that failure once, which is what makes
// it worth designing against rather than hoping about.
//
// Cuts land on turn boundaries ("## " headings) so the model is never handed
// half an exchange, which reads as a truncated thought rather than as a window
// onto a longer session.
func sampleTranscript(transcript string) string {
	if len(transcript) <= maxNotesInput {
		return transcript
	}
	head := maxNotesInput / notesHeadShare
	tail := maxNotesInput - head

	front := transcript[:head]
	if i := strings.LastIndex(front, "\n## "); i > 0 {
		front = front[:i+1]
	}
	back := transcript[len(transcript)-tail:]
	if i := strings.Index(back, "\n## "); i >= 0 {
		back = back[i+1:]
	}
	return front + "\n(Turns from the middle of this record are omitted here.)\n\n" + back
}

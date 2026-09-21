package coder

import (
	"context"
	"errors"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// Session notes carry context *across* sessions: what the work was for, what
// was decided and why, what was left in flight, in a form a different
// conversation and a different model can start from.
//
// They used to carry it across a *restart* as well, because the conversation
// did not survive the process. It does now (restore.go), and resuming a
// session restores it rather than summarizing it — a lossy paraphrase beside
// the thing it paraphrases competes for attention and can contradict it.
//
// The three arguments this comment used to make against restoring a
// conversation are still the right ones to answer, and restore.go answers
// them. Cost and attention: a restored history is re-sent with every message,
// so it goes through compaction at restore time rather than waiting for a
// turn boundary. Attribution: messages labelled `assistant` are read by the
// next model as its own past self, "so it will rationalize and then defend
// choices it would never have made, with no seam anywhere to notice" — which
// is answered by putting a seam there when the model has changed. A
// single-vendor harness can lean on its models sharing dispositions; Strument
// is multi-vendor by design and cannot, which is why the seam is said out
// loud rather than assumed.
//
// What is left for notes is the job the arguments never applied to. Crossing
// from one conversation to another is not a restart: there is no history to
// restore, because the whole point is that it is a different thread. Notes are
// ~300 words instead of tens of thousands of tokens, they go stale gracefully
// where a verbatim history does not, and they assert nothing about who said
// what.

// notesTimeout is sideTimeout; the name stays for the doc comment above it.
const notesTimeout = sideTimeout

// maxNotesInput caps the transcript slice fed to the side model. The transcript
// grows without bound, and the recent end is what a resumed session needs — the
// older part is either already reflected in the notes being replaced or is
// history the code itself now carries.
const maxNotesInput = 24_000

// NotesWriter returns a function that writes session notes from a project's
// transcript, using the side model. Same shape as CommitMessenger, and for the
// same reasons: a plain function so the caller owns when it runs, and a record
// hook so the side request reaches the turn's accounting instead of being spent
// invisibly. It shares the side-call retry (side.go).
//
// It returns "" on any failure, with the reason beside it. Notes are a
// convenience; a session that cannot write them must still be a session. The
// error is carried out rather than dropped so a caller can say *why* there are
// none — "the model returned no notes" is a lie when the truth is that the call
// ran out of its own time budget.
func NotesWriter(cl llm.ModelClient, model *config.Model, record func(llm.Usage), out Output, clock Clock,
	report SideCallReporter,
) func(transcript string) (string, error) {
	return func(transcript string) (string, error) {
		transcript = strings.TrimSpace(transcript)
		if transcript == "" {
			return "", errors.New("the session record is empty")
		}
		transcript = sampleTranscript(transcript)

		ctx, cancel := context.WithTimeout(context.Background(), notesTimeout)
		defer cancel()

		answer, err := sendSide(ctx, cl, llm.Request{
			Model: model.Slug,
			Messages: []llm.Message{
				llm.TextMessage(llm.RoleSystem, prompts.SessionNotes),
				llm.TextMessage(llm.RoleUser, transcript),
			},
			// The side model's own reasoning setting, like the commit message
			// and the summary — see commit.go for why leaving it unset did not
			// mean what its comment said it meant.
			ReasoningEffort: model.Reasoning,
			Temperature:     model.Temperature,
			ExtraParams:     model.RequestExtraParams(),
		}, "session notes", out, clock, record, report)
		notes := strings.TrimSpace(answer)
		if notes == "" && err == nil {
			err = errors.New("the model returned no notes")
		}
		return notes, err
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

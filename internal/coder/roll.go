package coder

import (
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// Rolling the history instead of summarizing it: a trial mode
// (doc/experiments/2026-10-compaction-roll).
//
// Over budget, the oldest whole turns are dropped, and the user's own messages
// from them are carried forward word for word in one harness note. Nothing is
// written by a model, so nothing is paraphrased, invented, or continued instead
// of summarized — the failures doc/experiments/2026-10-compaction-prompt found
// in summaries — and there is no side call to pay for or to be rate-limited.
// What is lost is the earlier turns' own work, which a summary keeps a rough
// account of. Codex and Kimi keep the user's messages this way beside their
// summaries (doc/experiments/2026-10-compaction-survey); the Cyborgists' rolling
// contexts keep only the window.

// Compaction modes, set by the hidden --compaction flag.
const (
	CompactionSummary  = "summary"   // the default: clear old tool output, then summarize
	CompactionRoll     = "roll"      // drop old turns, carry the user's messages
	CompactionRollBare = "roll-bare" // drop old turns, carry nothing
)

// rollHistory drops the oldest turns until the rest fits under half the budget
// — the tail a fold keeps — and puts a note in their place. It reports whether
// it dropped anything.
func (c *Coder) rollHistory(budget int, keepSaid bool) bool {
	msgs := c.doneMessages
	count := c.Summarizer.count

	// A turn starts at a user message that is not the harness's own; cutting
	// there never separates a tool call from its result.
	var starts []int
	for i, m := range msgs {
		if m.Role == llm.RoleUser && !strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			starts = append(starts, i)
		}
	}

	cut := -1
	for _, k := range starts {
		if k == 0 {
			continue
		}
		said := append(slices.Clone(c.rolledSaid), userTexts(msgs[:k])...)
		note := rollNote(said, budget, keepSaid, c.offersRecall())
		if count(note)+c.Summarizer.total(msgs[k:]) <= budget/2 {
			cut = k
			break
		}
	}
	if cut < 0 && len(starts) > 0 && starts[len(starts)-1] > 0 {
		// Nothing short of the last turn fits; keep the last turn whole.
		cut = starts[len(starts)-1]
	}
	if cut <= 0 {
		return false
	}

	before, beforeN := c.Summarizer.total(msgs), len(msgs)
	c.rolledSaid = append(c.rolledSaid, userTexts(msgs[:cut])...)
	note := rollNote(c.rolledSaid, budget, keepSaid, c.offersRecall())
	out := append([]llm.Message{note}, msgs[cut:]...)
	after := c.Summarizer.total(out)
	if after >= before {
		return false
	}
	c.doneMessages = out
	c.turns, c.compactedTurns = nil, true
	c.Out.Printf("Dropped the oldest %d of %d messages from the chat history (%d -> %d tokens)%s.",
		beforeN-len(msgs[cut:]), beforeN, before, after,
		map[bool]string{true: "; what the user said in them is kept", false: ""}[keepSaid])
	return true
}

// userTexts is the text of the user's own messages among msgs, oldest first,
// leaving out the harness's notes — including an earlier roll's, whose
// messages are already in rolledSaid.
func userTexts(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == llm.RoleUser && !strings.HasPrefix(m.Text(), llm.HarnessMarker) {
			if t := strings.TrimSpace(m.Text()); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// minRollNoteCap is the smallest the roll note's quotes may be capped to. A
// quarter of a small budget is a few messages; Kimi caps the user's carried
// messages at 20k tokens. The trial's pilot (doc/experiments/2026-10-compaction-roll)
// lost every early message to a quarter of a 4k budget, which no real window
// would impose.
const minRollNoteCap = 16384

// rollNote is the harness note that stands for the dropped turns. With
// keepSaid it quotes the user's messages from them, newest kept when they
// exceed a quarter of the budget (or minRollNoteCap, whichever is more), and
// says how many were left out.
func rollNote(said []string, budget int, keepSaid, recall bool) llm.Message {
	if !keepSaid {
		return llm.HarnessNote(prompts.RollBareLabel)
	}
	limit := max(budget/4, minRollNoteCap)
	var kept []string
	total := 0
	for _, s := range slices.Backward(said) {
		t := RuneCounter{}.Count(s) + 2
		if total+t > limit && len(kept) > 0 {
			break
		}
		kept = append(kept, s)
		total += t
	}
	slices.Reverse(kept)
	var b strings.Builder
	if recall {
		b.WriteString(prompts.RollRecallLabel)
	} else {
		b.WriteString(prompts.RollLabel)
	}
	if omitted := len(said) - len(kept); omitted > 0 {
		b.WriteString(prompts.RollOmitted(omitted))
	}
	for _, s := range kept {
		b.WriteString("\n\n> ")
		b.WriteString(strings.ReplaceAll(s, "\n", "\n> "))
	}
	if recall {
		b.WriteString("\n\n")
		b.WriteString(prompts.RollRecallTail)
	}
	return llm.HarnessNote(b.String())
}

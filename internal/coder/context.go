package coder

import (
	"strconv"
	"strings"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// ViewContext renders the chat the way the model currently reads it: the
// compaction summaries (detected by the SummaryLabel prefix) in order, then the
// live tail that the summaries do not cover. A positive n caps the number of
// summaries shown; a n <= 0 shows them all.
//
// The summaries and the live tail both live in the done/cur slots — the settled
// history and the current turn. The system prompt, examples, and notes are
// scaffolding the model reads every turn rather than history it is asked to
// remember, so they are not part of this view.
//
// /tokens says how full the window is and the transcript says what was actually
// said; what neither shows is the fold — the thing the model sees — which is
// the niche this command fills. It is deliberately a single mode: the fold is
// the fold, there is no second half to hide.
func (c *Coder) ViewContext(n int) string {
	chunks := c.formatMessages()

	var summaries []llm.Message
	var tail []llm.Message
	for _, m := range chunks.done {
		if isSummaryMessage(m) {
			summaries = append(summaries, m)
		} else {
			tail = append(tail, m)
		}
	}
	tail = append(tail, chunks.cur...)

	var b strings.Builder
	b.WriteString("Context as the model sees it:\n")

	if len(summaries) == 0 {
		b.WriteString("No compaction summaries; the whole history is the live tail.\n")
	} else if n > 0 && n < len(summaries) {
		word := "summaries"
		if n == 1 {
			word = "summary"
		}
		b.WriteString("first " + strconv.Itoa(n) + " of " + strconv.Itoa(len(summaries)) + " " +
			word + " shown.\n")
	}

	shown := summaries
	if n > 0 && n < len(summaries) {
		shown = summaries[:n]
	}
	for _, s := range shown {
		b.WriteString(s.Text())
		b.WriteString("\n")
	}

	if len(tail) > 0 {
		b.WriteString("\nLive tail:\n")
		b.WriteString(renderTail(tail))
	}
	return b.String()
}

// isSummaryMessage reports whether a message is a compaction summary: a system
// message carrying the SummaryLabel prefix. An earlier summary is itself a
// system message, so this must check by content, not by role alone — otherwise
// every earlier summary would be silently dropped on a later fold.
func isSummaryMessage(m llm.Message) bool {
	return m.Role == llm.RoleSystem && strings.HasPrefix(m.Text(), prompts.SummaryLabel)
}

// renderTail lays out the message slice as the model reads it — the turns the
// summaries do not cover.
func renderTail(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := strings.ToUpper(m.Role)
		body := m.Text()
		if body == "" && len(m.ToolCalls) > 0 {
			var calls strings.Builder
			for _, tc := range m.ToolCalls {
				calls.WriteString("calls ")
				calls.WriteString(tc.Name)
				calls.WriteByte('\n')
			}
			body = strings.TrimRight(calls.String(), "\n")
		}
		if body == "" {
			continue
		}
		b.WriteString(role + ":\n")
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

package coder

import (
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// ViewContext renders the chat the way the model currently reads it: the
// settled history, a roll's note standing for any turns compaction dropped,
// then the turn in progress.
//
// The system prompt is scaffolding the model reads every turn rather than
// history it is asked to remember, so it is not part of this view.
//
// /tokens says how full the window is and the transcript says what was actually
// said; what neither shows is the fold — the thing the model sees — which is
// the niche this command fills.
//
// It used to list compaction summaries first and take a count of them to
// show; the summaries went with the side model that wrote them.
func (c *Coder) ViewContext() string {
	chunks := c.formatMessages()
	msgs := append(slices.Clone(chunks.done), chunks.cur...)
	if len(msgs) == 0 {
		return "Context as the model sees it: no conversation yet.\n"
	}
	return "Context as the model sees it:\n\n" + renderTail(msgs)
}

// renderTail lays out the message slice as the model reads it.
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

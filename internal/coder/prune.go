package coder

import (
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// Clearing old tool output, before any summary is written.
//
// Most of a long session's history is tool results: files read, searches,
// command output. They are also the part that can be had again — the call
// that produced each one stays in the history, so the model can see what it
// asked for and ask again. Claude Code clears older tool outputs before it
// summarizes; OpenCode and DeepSeek Harness prune them the same way
// (doc/experiments/2026-10-compaction-survey). Doing it first means a history
// over budget often needs no summary at all, and everything the user and the
// model said stays word for word — which no summary manages
// (doc/experiments/2026-10-compaction-prompt).

// clearedToolResult replaces a cleared result's text.
const clearedToolResult = "[This result was cleared from the history to save space. " +
	"The call is above; make it again if the output is needed.]"

// pruneToolResults clears the text of tool results outside the most recent
// half of the budget, oldest first, and reports how many results it cleared
// and how many tokens that freed. It clears nothing unless that frees at least
// an eighth of the budget: each clearing rewrites the cached prefix, as a fold
// does, so it is not worth doing for a little.
//
// The protected tail is the same half-budget the fold keeps, so what the
// current work is reading stays intact.
func pruneToolResults(msgs []llm.Message, budget int, count func(llm.Message) int) (int, int) {
	protect := budget / 2
	tail := 0
	start := len(msgs)
	for i, m := range slices.Backward(msgs) {
		if tail+count(m) > protect {
			break
		}
		tail += count(m)
		start = i
	}

	cleared := llm.TextContent(clearedToolResult)
	clearedTokens := count(llm.Message{Role: llm.RoleTool, Content: cleared})
	var idx []int
	freed := 0
	for i := range start {
		m := msgs[i]
		if m.Role != llm.RoleTool || strings.HasPrefix(m.Text(), clearedToolResult) {
			continue
		}
		if gain := count(m) - clearedTokens; gain > 0 {
			idx = append(idx, i)
			freed += gain
		}
	}
	if freed < budget/8 {
		return 0, 0
	}
	for _, i := range idx {
		msgs[i].Content = cleared
	}
	return len(idx), freed
}

// pruneHistory clears old tool output from the settled history, says so, and
// reports whether the history now fits budget.
func (c *Coder) pruneHistory(budget int) bool {
	// A copy, so that a slice sharing the old backing array keeps its results.
	msgs := slices.Clone(c.doneMessages)
	n, freed := pruneToolResults(msgs, budget, c.Summarizer.count)
	if n == 0 {
		return false
	}
	c.doneMessages = msgs
	c.Out.Printf("Cleared %s from the chat history (about %d tokens); the calls are kept.",
		plural(n, "old tool result", "old tool results"), freed)
	return !c.Summarizer.tooBig(c.doneMessages, budget)
}

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

// clearedPrefix begins every cleared result, whichever placeholder replaced
// it; it is how a result already cleared is recognized.
const clearedPrefix = "[This result was cleared from the history to save space."

// clearedToolResult replaces a cleared result's text.
const clearedToolResult = clearedPrefix + " The call is above; make it again if the output is needed.]"

// clearedToolResultRecall replaces it when the recall tool is offered: the
// output is in the record, and the placeholder is where its loss is noticed,
// so it names the search first and re-running for the current output second.
// See prompts.RollRecallLabel for why.
const clearedToolResultRecall = clearedPrefix + " To see what it returned, search the record with " +
	"recall, using from=\"all\"; to get the current output, make the call above again.]"

// clearedPlaceholder is the placeholder this session uses.
func (c *Coder) clearedPlaceholder() string {
	if c.offersRecall() {
		return clearedToolResultRecall
	}
	return clearedToolResult
}

// pruneToolResults clears the text of tool results outside the most recent
// half of the budget, oldest first, and reports how many results it cleared
// and how many tokens that freed. It clears nothing unless that frees at least
// an eighth of the budget: each clearing rewrites the cached prefix, as a fold
// does, so it is not worth doing for a little.
//
// The protected tail is the same half-budget the fold keeps, so what the
// current work is reading stays intact.
func pruneToolResults(msgs []llm.Message, budget int, count func(llm.Message) int, placeholder string) (int, int) {
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

	cleared := llm.TextContent(placeholder)
	clearedTokens := count(llm.Message{Role: llm.RoleTool, Content: cleared})
	var idx []int
	freed := 0
	for i := range start {
		m := msgs[i]
		if m.Role != llm.RoleTool || strings.HasPrefix(m.Text(), clearedPrefix) {
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
	n, freed := pruneToolResults(msgs, budget, c.Summarizer.count, c.clearedPlaceholder())
	if n == 0 {
		return false
	}
	c.doneMessages = msgs
	c.Out.Printf("Cleared %s from the chat history (about %d tokens); the calls are kept.",
		plural(n, "old tool result", "old tool results"), freed)
	return !c.Summarizer.tooBig(c.doneMessages, budget)
}

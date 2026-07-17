package coder

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// This file is the REPL-facing session surface (basecoder-spec §1.2, §1.4):
// slash commands own their I/O and mutate chat state through these methods.

// ChatFiles returns the editable chat files, root-relative and sorted.
func (c *Coder) ChatFiles() []string { return c.inchatRelativeFiles() }

// ReadOnlyFiles returns the read-only reference files, root-relative and
// sorted.
func (c *Coder) ReadOnlyFiles() []string {
	out := make([]string, 0, len(c.absReadOnlyFnames))
	for _, f := range c.absReadOnlyFnames {
		out = append(out, c.relFname(f))
	}
	sort.Strings(out)
	return out
}

// DropFile removes a file (editable or read-only) from the chat by
// absolute or root-relative path; it reports whether anything was dropped.
func (c *Coder) DropFile(path string) bool {
	abs := c.absRootPath(path)
	if i := slices.Index(c.absFnames, abs); i >= 0 {
		c.absFnames = slices.Delete(c.absFnames, i, i+1)
		return true
	}
	if i := slices.Index(c.absReadOnlyFnames, abs); i >= 0 {
		c.absReadOnlyFnames = slices.Delete(c.absReadOnlyFnames, i, i+1)
		return true
	}
	return false
}

// DropAll removes every file from the chat.
func (c *Coder) DropAll() {
	c.absFnames = nil
	c.absReadOnlyFnames = nil
}

// ClearHistory forgets the conversation (both rotated and current
// messages); files stay in the chat.
func (c *Coder) ClearHistory() {
	c.doneMessages = nil
	c.curMessages = nil
}

// AppendExchange records a user/assistant pair in the current history
// without sending anything — the /run command's "add output to the chat"
// path, mirroring the §6.2 shape for model-proposed shell output.
func (c *Coder) AppendExchange(user, assistant string) {
	c.curMessages = append(c.curMessages,
		llm.TextMessage("user", user),
		llm.TextMessage("assistant", assistant),
	)
}

// SetModel switches the chat to a different model (/model). The caller
// swaps the Client to match the model's provider.
func (c *Coder) SetModel(m *config.Model) {
	c.Model = m
	c.Prompts = promptsForFormat(m.EditFormat)
}

// LastCommitHash is the short hash of the session's last auto-commit ("" if
// none); the REPL uses it for the undo hint.
func (c *Coder) LastCommitHash() string { return c.lastCommitHash }

// IsSessionCommit reports whether the short hash is one of this session's
// auto-commits (/undo refuses anything else, like aider's
// aider_commit_hashes gate).
func (c *Coder) IsSessionCommit(short string) bool { return c.sessionCommits[short] }

// CommitsBeforeMessage returns the HEAD hashes captured at the start of
// each message (§1.3); /diff uses the last one as its base.
func (c *Coder) CommitsBeforeMessage() []string { return c.commitBeforeMessage }

// RepoMapNow renders the repo map as the next send would see it (/map).
func (c *Coder) RepoMapNow() string { return c.repoMapContent() }

// TokensReport summarizes approximate context usage per assembly section
// (§10: counts are advisory; the default counter is runes/4).
func (c *Coder) TokensReport() string {
	chunks := c.formatMessages()

	rows := []struct {
		name string
		n    int
	}{
		{"system messages", c.countMessages(chunks.system) + c.countMessages(chunks.reminder)},
		{"examples", c.countMessages(chunks.examples)},
		{"repo map", c.countMessages(chunks.repo)},
		{"read-only files", c.countMessages(chunks.readonlyFiles)},
		{"chat files", c.countMessages(chunks.chatFiles)},
		{"chat history", c.countMessages(chunks.done) + c.countMessages(chunks.cur)},
	}

	var b strings.Builder
	b.WriteString("Approximate context usage, in tokens (estimated):\n")
	total := 0
	for _, row := range rows {
		total += row.n
		fmt.Fprintf(&b, "%8d  %s\n", row.n, row.name)
	}
	fmt.Fprintf(&b, "%8d  total", total)
	if c.Model.Context > 0 {
		fmt.Fprintf(&b, " of %d context window", c.Model.Context)
	}
	if c.sessionKnown {
		fmt.Fprintf(&b, "\nSession cost: $%.4f", c.totalCost)
	}
	return b.String()
}

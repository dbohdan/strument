package coder

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// This file is the REPL-facing session surface:
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

// DropUnder removes the exact file at path and every chat/read-only file
// beneath it when path is a directory, resolving path the same way AddFile
// does. It returns the root-relative names dropped, sorted.
func (c *Coder) DropUnder(path string) []string {
	dir := c.absRootPath(path)
	prefix := dir + string(filepath.Separator)
	var dropped []string
	filter := func(list []string) []string {
		kept := make([]string, 0, len(list))
		for _, abs := range list {
			if abs == dir || strings.HasPrefix(abs, prefix) {
				dropped = append(dropped, c.relFname(abs))
			} else {
				kept = append(kept, abs)
			}
		}
		return kept
	}
	c.absFnames = filter(c.absFnames)
	c.absReadOnlyFnames = filter(c.absReadOnlyFnames)
	sort.Strings(dropped)
	return dropped
}

// TrackedFiles returns the git-tracked files (repo-root-relative), or nil
// outside a repository — the file set /add expands a directory to.
func (c *Coder) TrackedFiles() []string {
	if c.Repo == nil {
		return nil
	}
	return c.Repo.TrackedFiles()
}

// ClearHistory forgets the conversation (both rotated and current
// messages); files stay in the chat.
func (c *Coder) ClearHistory() {
	c.doneMessages = nil
	c.curMessages = nil
}

// AppendExchange records a user/assistant pair in the current history
// without sending anything — the /run command's "add output to the chat"
// path, mirroring the shape for model-proposed shell output.
func (c *Coder) AppendExchange(user, assistant string) {
	c.curMessages = append(c.curMessages,
		llm.TextMessage("user", user),
		llm.TextMessage("assistant", assistant),
	)
}

// SetModel switches the chat to a different model (/model). The caller
// swaps the Client to match the model's provider. Switching models resets
// the active edit format to the new model's default (leaving ask mode, if
// any).
func (c *Coder) SetModel(m *config.Model) {
	c.Model = m
	c.SetEditFormat(m.EditFormat)
}

// EditFormat returns the active edit format.
func (c *Coder) EditFormat() string { return c.editFormat }

// SetEditFormat switches the active edit format and its prompt set without
// changing the model — the mechanism behind /ask and /code. An empty
// format restores the model's default.
func (c *Coder) SetEditFormat(format string) {
	if format == "" {
		format = c.Model.EditFormat
	}
	c.editFormat = format
	c.Prompts = promptsForFormat(format)
}

// LastCommitHash is the short hash of the session's last auto-commit ("" if
// none); the REPL uses it for the undo hint.
func (c *Coder) LastCommitHash() string { return c.lastCommitHash }

// IsSessionCommit reports whether the short hash is one of this session's
// auto-commits (/undo refuses anything else, like aider's
// aider_commit_hashes gate).
func (c *Coder) IsSessionCommit(short string) bool { return c.sessionCommits[short] }

// NoteUndo records in the chat history that the user reverted a turn's edits.
//
// Without it the model reads its own "Applied the edit to x.go" in the history
// and builds on a change that is no longer on disk. Nothing else tells it:
// /undo moves HEAD, restores files, and pops the snapshot stack, all outside
// any tool call it could be answered through.
//
// A user-role message is the honest shape here, and it is not the synthetic
// turn this harness otherwise refuses to write: the user really did type /undo.
// The assistant line after it only keeps the roles alternating.
func (c *Coder) NoteUndo(files []string) {
	if len(files) == 0 {
		return
	}
	c.doneMessages = append(c.doneMessages,
		llm.TextMessage("user", "I ran /undo. The edits from that turn are gone and "+
			strings.Join(files, ", ")+" are back to what they were before it. "+
			"Don't assume anything you changed there is still in place; read a file before building on it."),
		llm.TextMessage("assistant", "Understood. I'll treat those files as unchanged by that turn."))
}

// CommitsBeforeMessage returns the HEAD hashes captured at the start of
// each message; /diff uses the last one as its base.
func (c *Coder) CommitsBeforeMessage() []string { return c.commitBeforeMessage }

// RepoMapNow renders the repo map as the next send would see it (/map).
func (c *Coder) RepoMapNow() string { return c.repoMapContent() }

// RepoMapTokens is the repo-map token budget, or 0 when the map is off; the
// opening banner reports it.
func (c *Coder) RepoMapTokens() int {
	if c.RepoMap == nil {
		return 0
	}
	return c.RepoMap.MapTokens
}

// SessionCost returns the running session cost and whether any cost was
// priced this session; the history writer diffs it across a turn.
func (c *Coder) SessionCost() (usd float64, known bool) {
	return c.totalCost, c.sessionKnown
}

// SessionTokens returns the running session token totals; the history
// writer diffs them across a turn.
func (c *Coder) SessionTokens() (sent, received int) {
	return c.totalTokensSent, c.totalTokensReceived
}

// TokensReport summarizes approximate context usage per assembly section
// (counts are advisory; the default counter is runes/4).
func (c *Coder) TokensReport() string {
	chunks := c.formatMessages()

	rows := []struct {
		name string
		n    int
	}{
		{"system messages", c.countMessages(chunks.system) + c.countMessages(chunks.reminder)},
		{"examples", c.countMessages(chunks.examples)},
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

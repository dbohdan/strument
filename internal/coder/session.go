package coder

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/skill"
)

// This file is the REPL-facing session surface:
// slash commands own their I/O and mutate chat state through these methods.

// ChatFiles returns the editable chat files, root-relative and sorted.
func (c *Coder) ChatFiles() []string { return c.inchatRelativeFiles() }

// TurnEditedFiles returns what the turn just finished changed, sorted.
//
// It stays valid until the next turn starts: turnEditedFiles is reset at the
// top of initBeforeMessage, so a caller reading it after Run returns is reading
// the turn that just ended. That is what lets the transcript record a turn's
// files without the coder knowing a transcript exists.
func (c *Coder) TurnEditedFiles() []string { return slices.Sorted(maps.Keys(c.turnEditedFiles)) }

// LastOutcome is how the most recent send ended.
//
// Exported for the REPL, which says something different after an interrupted
// turn than after a finished one: an interruption looks like a kill and is not,
// and the difference is invisible from the outside.
func (c *Coder) LastOutcome() SendOutcome { return c.lastSendOutcome }

// ReadOnlyFiles returns the read-only reference files, root-relative and
// sorted.
func (c *Coder) ReadOnlyFiles() []string {
	out := make([]string, 0, len(c.absReadOnlyFnames))
	for _, f := range c.absReadOnlyFnames {
		out = append(out, c.displayName(f))
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
				dropped = append(dropped, c.displayName(abs))
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

// AppendContext adds material to the current history without sending
// anything — the "add output to the chat" path behind /run and /web.
//
// One message, not a pair. It used to append a fabricated "Ok." in the
// assistant's voice so the roles alternated, which bought nothing: two
// consecutive user messages are accepted by every provider, and a reply the
// model never gave is the one thing worth not writing. The user turn itself is
// honest — the user chose to add this — so it carries the material as typed.
//
// Deliberately unmarked, unlike the harness's own notes. This is not the
// harness speaking; it is material, and the "Command: … Output: …" shape the
// callers build says what it is. A marker claiming otherwise would blur the one
// distinction the marker exists to make.
func (c *Coder) AppendContext(text string) {
	c.curMessages = append(c.curMessages, llm.TextMessage(llm.RoleUser, text))
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
	// Config-provided examples (example_messages) ride on top of whatever
	// format's set is active, so they are re-applied on every switch. They are
	// experimental-arm input (the shell-parallelism trial's EX arm), not part
	// of any built-in set — which is why they live on the Coder rather than in
	// prompts.Set.
	for _, ex := range c.Examples {
		c.Prompts.ExampleMessages = append(c.Prompts.ExampleMessages, prompts.Example{
			Role: ex.Role, Content: ex.Content,
		})
	}
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
// The action was the user's — they really did type /undo — but the sentence is
// the harness's, so it goes out marked as such rather than in the user's voice.
// It used to be followed by a fabricated "Understood, I'll treat those files as
// unchanged", which the model never said; the alternation that line existed for
// is not something any provider requires.
// Two things the note used to get wrong, both found by the prompt review in
// doc/experiments/2026-08-prompt-review.md.
//
// It said "the edits from that turn are gone", and a turn is not the unit —
// nor is "that turn" a phrase the reader can resolve. /undo pops the snapshot
// stack, so the reverted work may be several messages back, and a model that
// has just finished a question-answering turn reads "that turn" as the one it
// just did, which changed nothing.
// settleEdits pushes a snapshot per commit, so a turn that called commit twice
// left three snapshots and one /undo pops one of them — the design says so
// itself ("/undo steps through the two halves one at a time"). A model told the
// turn was reverted, when only its last part was, reapplies work that is still
// there.
//
// And the verb was welded to the plural by strings.Join, so the single-file
// case — the common one — read "widget.go are back to what they were". All five
// reviewers caught that; only one caught the sentence above it, which is the
// one that can cost something.
func (c *Coder) NoteUndo(files []string) {
	if len(files) == 0 {
		return
	}
	subject := files[0] + " is back to what it was"
	if len(files) > 1 {
		subject = strings.Join(files, ", ") + " are back to what they were"
	}
	c.doneMessages = append(c.doneMessages, llm.HarnessNote(
		"The user ran /undo. Strument's most recent batch of edits is reverted, and "+
			subject+" before it. Anything Strument committed before that batch is still in place. "+
			"Don't assume anything you changed there is still in place; read a file before building on it."))
}

// CommitsBeforeMessage returns the HEAD hashes captured at the start of
// each message; /diff uses the last one as its base.
func (c *Coder) CommitsBeforeMessage() []string { return c.commitBeforeMessage }

// HasParser reports whether the tree-sitter layer is available: it is what
// symbol, /symbol, and the after-an-edit parse check are built on, and the
// banner says so. One condition, the same one toolDefs and SymbolLookup use, so
// the banner cannot claim something the tools disagree with.
//
// It replaced RepoMapTokens, which reported a token budget that stopped meaning
// anything when the map left the prompt: nothing spends those tokens.
func (c *Coder) HasParser() bool { return c.RepoMap != nil }

// SkillCounts reports how many skills this session can use and how many it
// found but may not.
//
// It exists for the same reason HasParser does: the banner must read the
// condition the tools read, or it can announce a capability the model does not
// have. usable is exactly what skillTool is built from.
func (c *Coder) SkillCounts() (usable, untrusted int) {
	return len(skill.Usable(c.Skills)), len(skill.Untrusted(c.Skills))
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
		{"system messages", c.countMessages(chunks.system)},
		// The schemas are not part of any message, so they were missing from
		// this table entirely while being sent with every single request.
		{"tool schemas", c.countTools()},
		{"examples", c.countMessages(chunks.examples)},
		{"session notes", c.countMessages(chunks.notes)},
		{"read-only files", c.countMessages(chunks.readonlyFiles)},
		// Pinned files no longer have a section of their own: their names ride
		// in the system prompt and their contents arrive as tool results, which
		// land in the history like any other tool result.
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
	// The rows above are what the *next* request will carry. The peak is the
	// largest one already sent, which the usage line cannot show: that line
	// sums over a turn's sends, and a turn that takes five steps re-sends its
	// conversation five times. The sum is the bill; this is the high-water mark.
	if c.peakTokensSent > 0 {
		fmt.Fprintf(&b, "\nLargest request so far: %d tokens (reported by the provider)", c.peakTokensSent)
	}
	if c.sessionKnown {
		fmt.Fprintf(&b, "\nSession cost: $%.4f", c.totalCost)
	}
	return b.String()
}

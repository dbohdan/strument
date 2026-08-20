package coder

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// commitTurn commits everything the turn changed, as one commit.
//
// One commit per turn, not one per send. The two things a commit can be — an
// undo substrate and a message to whoever reads the history later — used to be
// the same object, which worked while a turn was one send. It stopped working
// when the loop closed: a turn that edits across six steps wrote six commits,
// each described by a weak model that had seen only its own fragment. The
// substrate is the snapshot now (snapshot.go), so the commit can be just the
// communication.
//
// Waiting also improves the message for free: commitContext formats curMessages,
// which at turn end holds the user's request and the whole turn's work.
//
// A no-op without a repo, with auto-commits off, or in dry-run — the edits are
// still applied, and /undo still reaches them through the snapshot.
func (c *Coder) commitTurn() {
	if len(c.turnEditedFiles) == 0 || c.Repo == nil || !c.AutoCommits || c.DryRun {
		return
	}
	edited := slices.Sorted(maps.Keys(c.turnEditedFiles))

	hash, message, ok, err := c.Repo.Commit(edited, c.commitContext(), true)
	if err != nil {
		// A commit failure after the writes leaves the edits in the tree, where
		// /undo still reaches them through the turn's snapshot.
		c.Out.Errorf("Unable to commit: %v", err)
		return
	}
	if !ok {
		// The turn's edits netted out against HEAD — a change and its reversal,
		// or a rewrite of what was already there.
		c.Out.Toolf("The turn left the files as they were; nothing to commit.")
		return
	}

	c.lastCommitHash = hash
	if c.sessionCommits == nil {
		c.sessionCommits = map[string]bool{}
	}
	c.sessionCommits[hash] = true
	c.Out.Toolf("Commit %s %s", hash, message)
}

// commitContext formats curMessages for the commit-message model (aider's
// get_context_from_history).
//
// Tool calls are rendered, not only tool results. Message.Text() returns
// Content, and a model's calls live in the separate ToolCalls field, so
// formatting text alone gave the commit-message model the answers without the
// questions: the contents that came back from a read with no record of what was
// read or why, and none of the purpose strings the bash tool goes out of its way
// to require. In a harness where the whole of a turn's work arrives as tool
// calls, that is most of the turn.
//
// Arguments are capped hard. The commit model is handed the diff separately, so
// an edit call's arguments — the entire new text of a file — are the one thing
// here that is both enormous and already known. What the cap keeps is the
// leading, identifying part: the path, the query, the purpose.
const maxCommitArgs = 300

// maxCommitHistory bounds the earlier turns fed to the commit-message model.
// The tail is kept: the reason for a change is usually stated a turn or two
// before the change lands, not at the start of the session.
const maxCommitHistory = 8000

func (c *Coder) commitContext() string {
	prior := renderCommitMessages(c.doneMessages)
	if len(prior) > maxCommitHistory {
		prior = prior[len(prior)-maxCommitHistory:]
		if i := strings.IndexByte(prior, '\n'); i >= 0 {
			prior = prior[i+1:]
		}
		prior = "(Earlier conversation omitted.)\n" + prior
	}
	return prior + renderCommitMessages(c.curMessages)
}

func renderCommitMessages(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("\n" + strings.ToUpper(m.Role) + ": " + m.Text() + "\n")
		for _, tc := range m.ToolCalls {
			args := strings.Join(strings.Fields(tc.Arguments), " ")
			if len(args) > maxCommitArgs {
				args = args[:maxCommitArgs] + "…"
			}
			b.WriteString("CALL: " + tc.Name + " " + args + "\n")
		}
	}
	return b.String()
}

// commitMessageTimeout bounds the weak-model commit-message call; on
// timeout the commit proceeds with the fallback message.
const commitMessageTimeout = 60 * time.Second

// CommitMessenger returns a commit-message generator backed by a model,
// packaged as the git port's Message func. An empty return means "no message"
// and the caller falls back.
//
// record receives the request's usage, and exists because this call was
// spending money nobody could see: it goes out through the client directly, so
// it never reached finalizeUsage, and a measured turn reported $0.00084 having
// paid $0.00093. Nil is accepted for a caller that does not account.
func CommitMessenger(
	cl llm.ModelClient, model *config.Model, language string, record func(llm.Usage),
	out Output, clock Clock,
) func(diffs, context string) string {
	return func(diffs, chatContext string) string { //nolint:contextcheck // its own timeout; the turn's context is already done here.
		languageInstruction := ""
		if language != "" {
			languageInstruction = "\n- Is written in " + language + "."
		}
		system := pyFormat(prompts.CommitSystem, map[string]string{
			"language_instruction": languageInstruction,
		})

		content := ""
		if chatContext != "" {
			content = chatContext + "\n"
		}
		content += "# Diffs:\n" + diffs

		ctx, cancel := context.WithTimeout(context.Background(), commitMessageTimeout)
		defer cancel()

		answer, _ := sendSide(ctx, cl, llm.Request{
			Model: model.Slug,
			Messages: []llm.Message{
				llm.TextMessage("system", system),
				llm.TextMessage("user", content),
			},
			// No ReasoningEffort. It used to inherit the model's, so a reasoning
			// model would think its way to a subject line — paid for, invisible,
			// and slower at the one moment the user is waiting to get their
			// prompt back.
			Temperature: model.Temperature,
			ExtraParams: model.RequestExtraParams(),
		}, out, clock, record)
		return strings.TrimSpace(answer) // "" after exhausted retries => caller falls back
	}
}

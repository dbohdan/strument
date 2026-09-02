package coder

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/workspace"
)

// commitTurn commits everything the turn changed, as one commit.
//
// One commit per turn, not one per send. The two things a commit can be — an
// undo substrate and a message to whoever reads the history later — used to be
// the same object, which worked while a turn was one send. It stopped working
// when the loop closed: a turn that edits across six steps wrote six commits,
// each described by a side model that had seen only its own fragment. The
// substrate is the snapshot now (snapshot.go), so the commit can be just the
// communication.
//
// Waiting also improves the message for free: commitContext formats curMessages,
// which at turn end holds the user's request and the whole turn's work.
//
// A no-op without a repo, with auto-commits off, or in dry-run — the edits are
// still applied, and /undo still reaches them through the snapshot.
func (c *Coder) commitTurn(message string) {
	// What is new since the last commit, not what the turn has touched.
	//
	// These were the same set while a turn made one commit. They stopped being
	// the same when a turn could commit twice — first through an interrupt the
	// user steered, now through the commit tool — because turnEditedFiles
	// accumulates across the whole turn for the end-of-turn history record.
	// Handing git the earlier commit's paths is invisible while only Strument
	// is writing, since git commits what differs and those files no longer do.
	// It stops being invisible the moment the user edits one of them
	// themselves between two commits, and their work joins a model-authored
	// commit they never saw.
	edited := c.committablePaths(c.turnSnap.paths())
	if len(edited) == 0 || c.Repo == nil || !c.AutoCommits || c.DryRun {
		return
	}
	slices.Sort(edited)

	hash, message, ok, err := c.Repo.Commit(edited, c.commitContext(), message, true)
	if err != nil {
		// A commit failure after the writes leaves the edits in the tree, where
		// /undo still reaches them through the turn's snapshot.
		c.Out.Errorf("Unable to commit: %v", err)
		return
	}
	if !ok {
		// The turn's writes since the last settle net out against what is
		// committed — a change and its reversal, or a rewrite of what was
		// already there. Since the commit tool arrived, this can also be the
		// tail of a turn that already committed: the message must not say
		// "the turn left the files as they were", which is false the moment
		// the turn holds a commit.
		if c.lastCommitHash != "" {
			c.Out.Toolf("Nothing to commit since %s.", c.lastCommitHash)
		} else {
			c.Out.Toolf("The turn left the files as they were; nothing to commit.")
		}
		return
	}

	c.lastCommitHash = hash
	if c.sessionCommits == nil {
		c.sessionCommits = map[string]bool{}
	}
	c.sessionCommits[hash] = true
	c.Out.Toolf("Commit %s %s", hash, message)
}

// attributeShellCommits retro-attributes the commits a model-caused shell
// command made directly with git, when such a command moved HEAD: they get
// the trailer the commit tool would have added, and they join the session's
// commit records — /undo gates on those, and a commit the model made through
// bash is as undoable as one it made through the tool.
//
// before is the HEAD the coder saw before the command ran; empty means there
// was no repo to see, so there is nothing to attribute. A nil Repo is
// tolerated for the same reason observeCall tolerates nil: the watcher is
// off in sessions without git, not broken in them.
func (c *Coder) attributeShellCommits(before string) {
	if c.Repo == nil || before == "" || c.DryRun {
		return
	}
	trailer := c.Repo.TrailerValue()
	if trailer == "" {
		return
	}
	hashes, err := c.Repo.AttributeDirectCommits(before, trailer)
	if err != nil {
		// Said on screen only, not added to the tool result: the commits are
		// made and valid, and the failure is the session's bookkeeping, not
		// the command's outcome — a model that reads "attribution failed"
		// reacts by retrying the commit, which would make a second, worse
		// copy of the problem.
		c.Out.Errorf("Unable to attribute the commits made by the command: %v", err)
		return
	}
	if len(hashes) == 0 {
		return
	}
	if c.sessionCommits == nil {
		c.sessionCommits = map[string]bool{}
	}
	for _, h := range hashes {
		c.sessionCommits[h] = true
	}
	c.lastCommitHash = hashes[0] // newest first
	c.saveUndo()
	c.Out.Toolf("Attributed %d commit(s) the command made directly with git.", len(hashes))
}

// committablePaths splits the turn's writes into what git can record and what
// it cannot: repo-relative names pass through, and a path outside the
// repository — a scratch file under the platform temp directory, or an
// out-of-tree pinned file the turn edited — is dropped. It reports what it
// dropped, once, because a silent half-commit is the failure the readonly
// work documented: the model believes its work is recorded when only part of
// it is.
//
// The filter exists because git add with an outside path fails the *whole*
// commit, taking the in-repo edits' commit with it — one temp file in the
// batch would have cost the turn every commit it had. The snapshot keeps the
// dropped paths, so /undo still reaches them.
func (c *Coder) committablePaths(paths []string) []string {
	if c.Repo == nil {
		return paths
	}
	repoRoot := c.Repo.Root()
	if repoRoot == "" {
		// A Repo that does not report a root cannot be containment-checked;
		// pass everything through rather than silently committing nothing.
		// The real implementation always reports its root.
		return paths
	}
	var keep, dropped []string
	for _, p := range paths {
		if filepath.IsAbs(p) || !workspace.PathInRoot(repoRoot, p) {
			dropped = append(dropped, p)
			continue
		}
		keep = append(keep, p)
	}
	if len(dropped) > 0 {
		c.Out.Toolf("Not committing %s — outside the repository; /undo still covers them.",
			strings.Join(dropped, ", "))
	}
	return keep
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

// commitMessageTimeout bounds the side-model commit-message call; on
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

package coder

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/render"
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
//
// The error is git refusing the commit — a pre-commit hook, most often — and
// is returned as well as printed, because the commit tool has to tell the model
// why. Returning nothing left it to infer "nothing to commit" from the hash
// not moving, which is the one reading the hook's refusal rules out.
func (c *Coder) commitTurn(message string) error {
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
		return nil
	}
	slices.Sort(edited)

	hash, message, ok, err := c.Repo.Commit(edited, c.commitContext(), message, true)
	if err != nil {
		// A commit failure after the writes leaves the edits in the tree, where
		// /undo still reaches them through the turn's snapshot.
		c.Out.Errorf("Could not commit: %v", err)
		return err
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
		return nil
	}

	c.lastCommitHash = hash
	if c.sessionCommits == nil {
		c.sessionCommits = map[string]bool{}
	}
	c.sessionCommits[hash] = true
	c.Out.Toolf("Commit %s %s", hash, message)
	return nil
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
		c.Out.Errorf("Could not add model attribution to commits created by the command: %v", err)
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
	c.Out.Toolf("Added model attribution to %s created by the command.",
		render.Plural(len(hashes), "commit", "commits"))
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
	keep = c.dropAgentsLocal(keep)
	if len(dropped) > 0 {
		c.Out.Toolf("Not committing %s: outside the repository. These changes can still be restored with /undo.",
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
const commitMessageTimeout = sideTimeout

// commitInputCutNote marks a truncated diff, so the model reads it as cut
// rather than as a change that ends there — the same reason clipForSummary and
// maxToolOutputBytes announce their cuts.
//
// Phrased for its reader, which is the model writing the message. It first said
// the diff was "cut to fit the side model's context", which got the audience
// wrong twice over: "side model" is this codebase's name for a role, not
// something the model on the other end knows it occupies, and why the cut
// happened is not something it can act on. What it can act on is the scope of
// what it was given.
const commitInputCutNote = "\n… (cut; the rest of the diff is not part of this input — " +
	"describe only the changes shown)"

// commitCharsPerToken converts a token bound into a character budget.
//
// RuneCounter estimates at 4 characters per token, and its own comment says
// code runs closer to 3.3 — which is what a diff is. Everywhere else that
// estimate is advisory; here it decides what to cut, so it rounds the wrong way
// deliberately. At 3 the budget cuts a little more than it strictly must; at 4
// a dense diff slips through and the provider rejects the whole request, which
// is the failure this exists to prevent.
const commitCharsPerToken = 3

// commitContextOmittedNote marks a chat context cut to fit, matching the phrase
// commitContext already uses when maxCommitHistory trims the same material.
const commitContextOmittedNote = "(Earlier conversation omitted.)\n"

// fitCommitInput assembles the commit-message input within bound tokens,
// sacrificing the chat context before the diff.
//
// The order is the whole point. The prompt tells the model to "describe only
// what the diff does" and treats earlier turns as background, so when something
// has to go, background is what goes: a message written from a full diff and no
// context is worse-explained, while one written from full context and half a
// diff is wrong about what changed. The diff is cut only when it exceeds the
// budget by itself.
//
// Sized in characters against a token bound at commitCharsPerToken.
func fitCommitInput(chatContext, diffs string, bound int, out Output) string {
	budget := (bound - summaryInputBuffer) * commitCharsPerToken
	if budget <= 0 {
		return "# Diffs:\n" + diffs
	}

	body := "# Diffs:\n" + diffs
	if len(body) >= budget {
		// No room for context at all, and the diff itself must give.
		keep := budget - len(commitInputCutNote)
		if keep < len(body) && keep > 0 {
			out.Toolf("The diff is too large for the commit-message model; describing the first %s of it.",
				render.Plural(keep, "character", "characters"))
			return body[:keep] + commitInputCutNote
		}
		return body
	}
	if chatContext == "" {
		return body
	}

	if room := budget - len(body) - 1; room >= len(chatContext) { // -1 for the joining newline
		return chatContext + "\n" + body
	}
	// The marker is part of what gets sent, so it comes out of the budget
	// rather than being added on top of it — which is what it did first, and
	// what put the result 29 characters over a bound the whole function exists
	// to hold.
	room := budget - len(body) - 1 - len(commitContextOmittedNote)
	if room <= 0 {
		return body
	}
	// Keep the tail: the reason for a change is usually stated a turn or two
	// before it lands, which is the same reasoning maxCommitHistory follows.
	cut := chatContext[len(chatContext)-room:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 {
		cut = cut[i+1:]
	}
	return commitContextOmittedNote + cut + "\n" + body
}

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
	out Output, clock Clock, prompt string, report SideCallReporter,
) func(diffs, context string) string {
	return func(diffs, chatContext string) string { //nolint:contextcheck // its own timeout; the turn's context is already done here.
		languageInstruction := ""
		if language != "" {
			languageInstruction = "\n- Is written in " + language + "."
		}
		// prompt is the user's whole-string replacement for the commit system
		// prompt (`prompt_commit`), or the built-in when empty. Both are
		// pyFormat templates sharing the {language_instruction} slot.
		p := prompts.CommitSystem
		if prompt != "" {
			p = prompt
		}
		system := pyFormat(p, map[string]string{
			"language_instruction": languageInstruction,
		})

		content := fitCommitInput(chatContext, diffs, sideInputBound(model), out)

		ctx, cancel := context.WithTimeout(context.Background(), commitMessageTimeout)
		defer cancel()

		answer, _ := sendSide(ctx, cl, llm.Request{
			Model: model.Slug,
			Messages: []llm.Message{
				llm.TextMessage("system", system),
				llm.TextMessage("user", content),
			},
			// The side model's own reasoning setting, like the summary call.
			//
			// This field used to be left unset, with a comment claiming that
			// stopped a reasoning model from thinking its way to a subject
			// line. It did not: client.go reads "" as "defer to the provider
			// default; send nothing", so the model reasoned exactly as much as
			// it pleased and Strument merely gave up the ability to say
			// otherwise. Live, every one of four side models reasoned here —
			// one spent 36,141 characters of reasoning on a 202-character
			// commit message, taking 845.9s.
			//
			// Passing the configured effort through is what makes the comment's
			// intent reachable: `reasoning="off"` on the side model now turns it
			// off, where before the setting was ignored on this path. It is not
			// forced off here, because "off" is not universally accepted —
			// glm-5.3-flash rejects reasoning:{enabled:false} with HTTP 400 —
			// so the choice belongs in config, where the user knows their model.
			ReasoningEffort: model.Reasoning,
			Temperature:     model.Temperature,
			ExtraParams:     model.RequestExtraParams(),
		}, "commit message", out, clock, record, report)
		return strings.TrimSpace(answer) // "" after exhausted retries => caller falls back
	}
}

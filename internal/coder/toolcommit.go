package coder

import (
	"encoding/json"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// maxCommitSubject is where a summary line stops being a summary. Git's own
// convention is 50; this is loose enough not to argue with a model over a long
// scope prefix, and tight enough that a paragraph cannot arrive here.
const maxCommitSubject = 100

// commitTool lets the model close a batch of edits as one commit.
//
// It exists because a model already knows where its commit boundaries are and
// had no way to say so. MiMo, mid-task, wrote out a four-commit plan with the
// boundaries named and the tradeoff between two of them spelled out — and then
// put everything in one commit, because the turn boundary was the only commit
// boundary there was.
//
// The turn boundary does not move. The human still reviews in the same place;
// the diff simply arrives as several logical commits instead of one blob.
//
// The subject and body are separate arguments rather than one string so the
// shape is structural: a wall of text cannot land in the summary line, and the
// body — the *why*, which the diff cannot carry — has somewhere to go that the
// model has to decide about rather than trail off after.
func commitTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolCommit,
		Description: "Commit the edits you have made so far in this turn, as one commit. " +
			"Use it when your work has natural boundaries — a refactor, then the tests " +
			"it needed, then the docs — so each lands as its own reviewable change " +
			"instead of one undifferentiated diff.\n\n" +
			"Commits exactly the files your edit and write calls have changed since your " +
			"last commit. Files that a bash command changed — a formatter, a code " +
			"generator — are not included, and are left for you to edit or for the user " +
			"to commit.\n\n" +
			"Call it as you finish each part, not once at the end: make the edits for one " +
			"part and commit them in the same step, then start the next part when that " +
			"result comes back. Edits you make in one step all land in one commit, so a " +
			"turn that edits everything before committing can only produce a single " +
			"commit. Prefer boundaries where the tree still " +
			"builds; if a step cannot stand on its own, fold it into the next one rather " +
			"than committing something broken. Nothing here refuses a commit — the " +
			"judgment is yours.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subject": strProp("The summary line, in the imperative mood, " +
					"conventional-commit style (`fix(coder): …`). No trailing period."),
				"body": strProp("Optional. Why the change was made, and anything a " +
					"reader of the history could not recover from the diff — a decision " +
					"and its alternatives, a constraint that forced the shape. Skip it " +
					"when the subject genuinely says everything."),
			},
			"required": []string{"subject"},
		},
	}
}

// commitArgs is one parsed commit call.
type commitArgs struct {
	callID  string
	subject string
	body    string
}

// parseCommitArgs validates a commit call, returning an error result for the
// model when it cannot be used.
func parseCommitArgs(tc llm.ToolCall) (commitArgs, string) {
	var raw struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &raw); err != nil {
		return commitArgs{}, fmt.Sprintf("Could not parse the arguments: %v", err)
	}
	subject := strings.TrimSpace(raw.Subject)
	if subject == "" {
		return commitArgs{}, "A commit needs a subject line."
	}
	if strings.ContainsAny(subject, "\n\r") {
		return commitArgs{}, "The subject is one line. Put the rest in `body`."
	}
	if len(subject) > maxCommitSubject {
		return commitArgs{}, fmt.Sprintf(
			"The subject is %d characters; keep it under %d and move the detail into `body`.",
			len(subject), maxCommitSubject)
	}
	return commitArgs{callID: tc.ID, subject: subject, body: strings.TrimSpace(raw.Body)}, ""
}

// message assembles the git commit message: subject, blank line, body.
func (a commitArgs) message() string {
	if a.body == "" {
		return a.subject
	}
	return a.subject + "\n\n" + a.body
}

// runCommitTool commits what has been written since the last commit.
//
// Every way this can fail to commit answers the model in the result rather
// than refusing, because none of them is the model's mistake to fix: a session
// without git, with auto-commits off, or in dry-run is a session the user
// configured that way, and "nothing has changed" is a fact about the turn.
// Only a malformed call is worth a reflection, and parseCommitArgs handles
// those before this runs.
func (c *Coder) runCommitTool(args commitArgs) string {
	switch {
	case c.Repo == nil:
		return "There is no git repository, so nothing was committed. Your edits are applied to the files."
	case !c.AutoCommits:
		return "The user turned committing off for this session (--no-auto-commits), so nothing was committed. Your edits are applied to the files."
	case c.DryRun:
		return "This is a dry run: nothing was written, so there is nothing to commit."
	case c.turnSnap.empty():
		return "Nothing has been written since your last commit, so there was nothing to commit."
	}

	before := c.lastCommitHash
	c.settleEdits(args.message())
	if c.lastCommitHash == before {
		// commitTurn already told the user why. Say the same thing to the
		// model rather than letting it believe a commit it can name happened.
		return "Nothing was committed: the files match what is already committed."
	}
	return fmt.Sprintf("Committed %s: %s", c.lastCommitHash, args.subject)
}

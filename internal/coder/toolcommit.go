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

// commitTool is how the model's edits are committed: the only way, since a
// turn no longer ends in a commit of its own.
//
// It began as a way to split one turn's work into several commits. A model
// already knows where its commit boundaries are and had no way to say so:
// MiMo, mid-task, wrote out a four-commit plan with the boundaries named and
// the tradeoff between two of them spelled out — and then put everything in
// one commit, because the turn boundary was the only commit boundary there was.
//
// The description used to lead with that, "use it when your work has natural
// boundaries", which was right while the turn-end commit caught everything
// else. It reads as optional, and models treated it so: in the Featherweight
// trial's Strument fix sessions, with the turn-end commit still on, DeepSeek
// called it in 34 of 40 and MiMo in 14 of 40. With nothing behind it, the
// description has to say that committing finished work is the job, and only
// then how to split it.
//
// The subject and body are separate arguments rather than one string so the
// shape is structural: a wall of text cannot land in the summary line, and the
// body — the *why*, which the diff cannot carry — has somewhere to go that the
// model has to decide about rather than trail off after.
//
// The splitting example used to be "a refactor, then the tests it needed, then
// the docs", which is word for word the reach clause's own list — "the call
// sites it breaks, the tests that cover it, the docs that describe it". One
// says the triad is a single request; the other held it up as three commits.
// The example now names a boundary the reach clause does not claim.
func commitTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolCommit,
		Description: "Commit your edits. When a change is done, commit it with this tool, " +
			"with a message that says what changed and why. Nothing commits your work " +
			"for you: edits you do not commit stay in the working tree for the user to " +
			"commit.\n\n" +
			"A commit takes the files your edit and write calls have changed since your " +
			"last commit, in this turn or an earlier one, and the files your bash commands " +
			"staged with git — `git mv`, `git rm`, `git add` — each as it is on disk. Other " +
			"files a command changed, such as a formatter's or a code generator's output, " +
			"are left out unless you stage them. Changes the user had already made to those " +
			"files are committed separately first, so your commit holds only your work.\n\n" +
			"When the work has natural parts — a helper extracted, then the feature that " +
			"uses it — commit each part as you finish it, in the same step as its last " +
			"edit: edits made in one step land in one commit, so a turn that edits " +
			"everything before committing can only produce one. Prefer boundaries where " +
			"the tree still builds; if a part cannot stand on its own, fold it into the " +
			"next one rather than committing something broken.",
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
// without git, with commits off, or in dry-run is a session the user
// configured that way, and "nothing has changed" is a fact about the turn.
// Only a malformed call is worth a reflection, and parseCommitArgs handles
// those before this runs.
func (c *Coder) runCommitTool(args commitArgs) string {
	switch {
	case c.Repo == nil:
		return "There is no git repository, so nothing was committed. Your edits are applied to the files."
	case !c.Commits:
		return "The user turned committing off for this session, so nothing was committed. Your edits are applied to the files."
	case c.DryRun:
		return "This is a dry run: nothing was written, so there is nothing to commit."
	case !c.hasPending():
		return "Nothing has been written since your last commit, so there was nothing to commit."
	}

	c.settleMu.Lock()
	defer c.settleMu.Unlock()
	before := c.lastCommitHash
	c.setSettling(c.turnSnap)
	tc, err := c.commitTurn(args.message())
	c.setSettling(nil)
	if err != nil {
		// The writes stay pending, unlike settleEdits: the model can fix what
		// git objected to and call this again, and the commit has to find the
		// same edits when it does.
		return "The commit was refused, so nothing was committed. Your edits are still in the " +
			"files and will be included when a commit succeeds. Git said:\n" + err.Error()
	}
	c.pushTurnSnapshot()
	if c.lastCommitHash == before {
		// commitTurn already told the user why. Say the same thing to the
		// model rather than letting it believe a commit it can name happened.
		result := "Nothing was committed: the files match what is already committed."
		if len(tc.conflicted) > 0 {
			result += " " + conflictedNote(tc.conflicted)
		}
		return result
	}
	result := fmt.Sprintf("Committed %s: %s", c.lastCommitHash, args.subject)
	if len(tc.staged) > 0 {
		result += "\nIt includes what your shell commands staged with git: " + strings.Join(tc.staged, ", ") + "."
	}
	if len(tc.conflicted) > 0 {
		result += "\n" + conflictedNote(tc.conflicted)
	}
	return result
}

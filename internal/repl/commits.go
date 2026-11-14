package repl

import (
	"context"
	"strings"
)

// cmdCommits turns this session's commits off and on: whether the model's
// commit tool commits.
//
// It exists because --no-commits was a decision you could only make before
// starting, and the cases that want it arrive mid-session: deliberately breaking
// a template to test an XSS filter, trying an edit you intend to throw away,
// working in a tree whose history you do not want to author.
//
// Bare reports rather than toggles, which is the one place this parts company
// with /raw in Codex CLI and /yolo in Kimi Code. The house pattern is the
// stronger precedent — /model, /env, /notes and /context all report when given
// nothing — and a command that silently changes behaviour when you typed it to
// ask about behaviour is the "did that apply?" ambiguity this codebase keeps
// legislating against. `/commits off` is three characters more and says what it
// did. irssi draws the same line from the other side: /TOGGLE flips, but /SET
// reports, and this is a /SET.
//
// What it reports is what actually gates a commit (see Coder.commitTurn), not
// just the flag it sets. A setting that reads "on" while a dry run or a missing
// repository means nothing will be committed is a worse answer than no answer.
func cmdCommits(_ context.Context, r *REPL, args string) string {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "":
		r.printCommitState()
	case "on":
		if r.coder.Repo == nil {
			// Not recoverable mid-session: the repository handle is built at
			// startup, so there is nothing for a commit to go to. Say that
			// rather than setting a flag that cannot have an effect.
			r.out.Errorf("Git integration is disabled for this run, so there is nothing to commit to.")
			r.printf("  Start without --no-git, in a directory that is a git repository, to commit.")
			return ""
		}
		r.coder.Commits = true
		r.printCommitState()
	case "off":
		r.coder.Commits = false
		r.printCommitState()
	default:
		r.out.Errorf("%s", usage("commits"))
	}
	return ""
}

// printCommitState says whether a turn's edits will be committed, and when they
// will not, which of the three reasons applies.
func (r *REPL) printCommitState() {
	switch {
	case r.coder.Repo == nil:
		r.printf("Commits: off. Git integration is disabled for this run.")
		r.printf("  Edits are still written to the working tree, and /undo can still reverse a turn.")
	case r.coder.DryRun:
		// DryRun outranks the setting in commitTurn, so reporting the setting
		// alone would be true and useless.
		r.printf("Commits: off. This is a dry run, so no files are written either.")
	case r.coder.Commits:
		r.printf("Commits: on. The model commits its edits with its commit tool.")
	default:
		r.printf("Commits: off. Edits are written to the working tree, and the model's commit tool commits nothing.")
		r.printf("  /undo can still reverse a turn, and /diff still shows what changed.")
	}
}

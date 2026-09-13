package repl

import (
	"context"
	"strings"
)

// cmdCommits turns this session's auto-commits off and on.
//
// It exists because --no-auto-commits was a decision you could only make before
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
			r.out.Errorf("Git integration is off for this session, so there is nothing to commit to.")
			r.printf("  Start without --no-git, in a directory that is a git repository, to commit.")
			return ""
		}
		r.coder.AutoCommits = true
		r.printCommitState()
	case "off":
		r.coder.AutoCommits = false
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
		r.printf("Commits: off — git integration is not on for this session.")
		r.printf("  Edits still land in the working tree, and /undo still covers a turn.")
	case r.coder.DryRun:
		// DryRun outranks the setting in commitTurn, so reporting the setting
		// alone would be true and useless.
		r.printf("Commits: off — this is a dry run, so no file is written either.")
	case r.coder.AutoCommits:
		r.printf("Commits: on. Each turn that changes a file ends in a commit.")
	default:
		r.printf("Commits: off. Edits land in the working tree and are not committed.")
		r.printf("  /undo still covers a turn, and /diff still shows what changed.")
	}
}

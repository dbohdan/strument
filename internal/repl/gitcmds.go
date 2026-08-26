package repl

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// cmdUndo undoes the last turn.
//
// It means one turn because a turn is one commit (coder.commitTurn). With a
// repository this ports aider's raw_cmd_undo — restore the files of the last
// commit from its parent, move HEAD back soft — keeping its safety gates:
// session commit only, single parent, clean files, files present in the parent,
// not already pushed. Without one it restores the turn's snapshot instead.
//
// The two substrates are not interchangeable. Only git can move HEAD, and only
// git knows whether a commit has been pushed; only the snapshot exists in a
// directory that is not a repository. So the command has two paths, and both
// pop the same stack, so a session that has both never loses count.
func cmdUndo(_ context.Context, r *REPL, _ string) string {
	g := r.opts.Git
	if g == nil {
		return undoFromSnapshot(r)
	}

	sha, short, subject, parents, err := g.HeadInfo()
	if err != nil {
		r.out.Errorf("Unable to complete undo: %v", err)
		return ""
	}
	if parents == 0 {
		r.out.Errorf("This is the first commit in the repository. Cannot undo.")
		return ""
	}
	if !r.coder.IsSessionCommit(short) {
		r.out.Errorf("The last commit was not made by Strument in this chat session.")
		r.printf("You could try `git reset --hard HEAD^` but be aware that this is a destructive command!")
		return ""
	}
	if parents > 1 {
		r.out.Errorf("The last commit %s has more than 1 parent, can't undo.", sha)
		return ""
	}

	changed, err := g.ChangedInHead()
	if err != nil {
		r.out.Errorf("Unable to complete undo: %v", err)
		return ""
	}
	for _, f := range changed {
		if g.IsDirty(f) {
			r.out.Errorf("The file %s has uncommitted changes. Please stash them before undoing.", f)
			return ""
		}
		if !g.InCommit("HEAD^", f) {
			r.out.Errorf("The file %s was not in the repository in the previous commit. Cannot undo safely.", f)
			return ""
		}
	}

	if branch := g.CurrentBranch(); branch != "" {
		if remote, err := g.RevParse("origin/" + branch); err == nil && remote == sha {
			r.out.Errorf("The last commit has already been pushed to the origin. Undoing is not possible.")
			return ""
		}
	}

	var restored, unrestored []string
	for _, f := range changed {
		if err := g.CheckoutFileFrom("HEAD~1", f); err != nil {
			unrestored = append(unrestored, f)
		} else {
			restored = append(restored, f)
		}
	}
	if len(unrestored) > 0 {
		r.out.Errorf("Error restoring %s, aborting undo.", unrestored[len(unrestored)-1])
		r.printf("Restored files:")
		for _, f := range restored {
			r.printf("  %s", f)
		}
		r.printf("Unable to restore files:")
		for _, f := range unrestored {
			r.printf("  %s", f)
		}
		return ""
	}

	if err := g.ResetSoft("HEAD~1"); err != nil {
		r.out.Errorf("Unable to complete undo: %v", err)
		return ""
	}
	r.coder.DropTurnSnapshot() // the commit was the record here; keep the stacks level
	r.coder.NoteUndo(changed)  // or the model builds on edits that are no longer there
	r.printf("Removed: %s %s", short, subject)
	if _, nowShort, nowSubject, _, err := g.HeadInfo(); err == nil {
		r.printf("Now at:  %s %s", nowShort, nowSubject)
	}
	return ""
}

// undoFromSnapshot is /undo without a repository: put back what the last turn
// wrote. This is what makes Strument usable on a live configuration directory
// or under another SCM — the edits are as recoverable there as in a repo, just
// through a different record.
func undoFromSnapshot(r *REPL) string {
	restored, err := r.coder.UndoLastTurn()
	if err != nil {
		r.out.Errorf("Unable to complete undo: %v", err)
		return ""
	}
	r.coder.NoteUndo(restored)
	r.printf("Undid the last turn's edits:")
	for _, f := range restored {
		r.printf("  %s", f)
	}
	return ""
}

// cmdSquash folds the last n turn-commits into one, with a message written for
// the whole of it.
//
// One commit per turn is the right default granularity and the wrong one often
// enough: three turns that felt separate while you were driving read afterwards
// as a single change. Merging upward is safe — the human has seen the result
// and is naming a unit they recognize. Splitting downward is not, so Strument
// never tries to detect a boundary on its own.
//
// The gates are /undo's, for the same reason: every commit folded must be one
// this session made, none may have been pushed, and none of the files may carry
// uncommitted changes — a soft reset keeps the worktree, so a dirty file would
// otherwise be swept into the squash commit without being asked.
func cmdSquash(_ context.Context, r *REPL, args string) string {
	g := r.opts.Git
	if g == nil {
		r.out.Errorf("No git repository; edits are applied, not committed.")
		return ""
	}

	n := 2
	if arg := strings.TrimSpace(args); arg != "" {
		parsed, err := strconv.Atoi(arg)
		if err != nil || parsed < 2 {
			r.out.Errorf("%s, where n is 2 or more.", usage("squash"))
			return ""
		}
		n = parsed
	}

	commits, err := g.LastCommits(n)
	if err != nil {
		r.out.Errorf("Unable to squash: %v", err)
		return ""
	}
	if len(commits) < n {
		r.out.Errorf("There are only %d commits to squash.", len(commits))
		return ""
	}
	for _, c := range commits {
		if !r.coder.IsSessionCommit(c.Short) {
			r.out.Errorf("Commit %s was not made by Strument in this chat session.", c.Short)
			return ""
		}
	}

	base := fmt.Sprintf("HEAD~%d", n)
	if branch := g.CurrentBranch(); branch != "" {
		if remote, err := g.RevParse("origin/" + branch); err == nil {
			for _, c := range commits {
				if c.SHA == remote {
					r.out.Errorf("Commit %s has already been pushed to the origin. Squashing is not possible.", c.Short)
					return ""
				}
			}
		}
	}

	files, err := g.ChangedInRange(base)
	if err != nil {
		r.out.Errorf("Unable to squash: %v", err)
		return ""
	}
	if len(files) == 0 {
		r.out.Errorf("Those commits change nothing between them; there is nothing to squash.")
		return ""
	}
	for _, f := range files {
		if g.IsDirty(f) {
			r.out.Errorf("The file %s has uncommitted changes. Please commit or stash them before squashing.", f)
			return ""
		}
	}

	if err := g.ResetSoft(base); err != nil {
		r.out.Errorf("Unable to squash: %v", err)
		return ""
	}
	// Commit re-stages the files, diffs them, and asks the side model for a
	// message covering the whole range — the same path an ordinary turn takes.
	// The context is the subjects being replaced, so the new message describes
	// the same work rather than starting from the diff alone.
	var context strings.Builder
	context.WriteString("These commits are being combined into one:\n")
	for _, c := range slices.Backward(commits) {
		fmt.Fprintf(&context, "- %s\n", c.Subject)
	}
	hash, message, ok, err := g.Commit(files, context.String(), "", true)
	if err != nil || !ok {
		r.out.Errorf("The commits were folded back into the index but not committed: %v", err)
		r.printf("Your changes are staged; commit them yourself with `git commit`.")
		return ""
	}

	r.coder.SquashTurns(hash, n)
	r.printf("Squashed %d commits into %s %s", n, hash, message)
	return ""
}

// cmdDiff shows the working tree against the HEAD captured before the last
// message (aider's raw_cmd_diff, with the base taken from the coder's
// commit-before-message stack).
func cmdDiff(_ context.Context, r *REPL, _ string) string {
	g := r.opts.Git
	if g == nil {
		r.out.Errorf("No git repository found.")
		return ""
	}

	head := g.HeadSHA()
	if head == "" {
		r.out.Errorf("Unable to get current commit. The repository might be empty.")
		return ""
	}

	base := head + "^"
	if cbm := r.coder.CommitsBeforeMessage(); len(cbm) > 0 && cbm[len(cbm)-1] != "" {
		base = cbm[len(cbm)-1]
	}
	if base == head {
		r.out.Warningf("No changes to display since the last message.")
		return ""
	}

	r.printf("Diff since %.7s...", base)
	diff, err := g.DiffWorktree(base)
	if err != nil {
		r.out.Errorf("Unable to complete diff: %v", err)
		return ""
	}
	if strings.TrimSpace(diff) == "" {
		r.out.Warningf("No changes to display since the last message.")
		return ""
	}
	r.printf("%s", strings.TrimRight(diff, "\n"))
	return ""
}

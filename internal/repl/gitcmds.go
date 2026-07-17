package repl

import (
	"context"
	"strings"
)

// cmdUndo ports aider's raw_cmd_undo: restore the files of the last
// auto-commit from its parent and move HEAD back (soft), with the same
// safety gates — session commit only, single parent, clean files, files
// present in the parent, not already pushed.
func cmdUndo(_ context.Context, r *REPL, _ string) string {
	g := r.opts.Git
	if g == nil {
		r.out.Errorf("No git repository found.")
		return ""
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
		r.out.Errorf("The last commit was not made by strument in this chat session.")
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
	r.printf("Removed: %s %s", short, subject)
	if _, nowShort, nowSubject, _, err := g.HeadInfo(); err == nil {
		r.printf("Now at:  %s %s", nowShort, nowSubject)
	}
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

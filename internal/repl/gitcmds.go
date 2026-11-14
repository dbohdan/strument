package repl

import (
	"context"
	"strings"
)

// cmdUndo undoes the last turn: the files come back from the turn's snapshot,
// and the commits made during it come off the branch (Coder.UndoLastTurn).
//
// It used to mean one commit, with a repository, because a turn was one
// commit: aider's raw_cmd_undo, which restores the files of the last commit
// from its parent and moves HEAD back. Committing is the model's now, so a
// turn can end with no commit, or two, and the snapshot is the only record
// that knows which turn did what. The gates aider's version had stay, in the
// coder: the commits must be the newest on the branch, made in this session,
// unpushed, not merges, and the files they touch clean.
func cmdUndo(_ context.Context, r *REPL, _ string) string {
	res, err := r.coder.UndoLastTurn()
	if err != nil {
		r.out.Errorf("Could not undo: %v", err)
		return ""
	}
	r.coder.NoteUndo(res.Restored) // or the model builds on edits that are no longer there
	if len(res.Restored) > 0 {
		r.printf("Undid the last turn's edits:")
		for _, f := range res.Restored {
			r.printf("  %s", f)
		}
	}
	if len(res.Removed) > 0 {
		r.printf("Removed %s: %s", plural(len(res.Removed), "commit", "commits"), strings.Join(res.Removed, ", "))
		if g := r.opts.Git; g != nil {
			if _, short, subject, _, err := g.HeadInfo(); err == nil {
				r.printf("Now at:  %s %s", short, subject)
			}
		}
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
		r.out.Errorf("Could not read the current commit; the repository may be empty.")
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
		r.out.Errorf("Could not show the diff: %v", err)
		return ""
	}
	if strings.TrimSpace(diff) == "" {
		r.out.Warningf("No changes to display since the last message.")
		return ""
	}
	r.printf("%s", strings.TrimRight(diff, "\n"))
	return ""
}

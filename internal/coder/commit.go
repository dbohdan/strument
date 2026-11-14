package coder

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/workspace"
)

// commitTurn commits the model's work since its last commit, as one commit
// with the model's message. It is the commit tool's, the only way the model's
// edits are committed: a turn no longer ends in one. See settleEdits.
//
// What is committed is what the model has written since it last committed,
// which can span turns — pendingEdits holds what earlier settles closed,
// turnSnap what this stretch of the turn wrote — and what its shell commands
// staged (staging.go). Handing git a path the model did not write since then
// would be invisible while only Strument is writing, since git commits what
// differs and those files no longer do. It stops being invisible the moment
// the user edits one of them, and their work joins a model-authored commit
// they never saw.
//
// A no-op without a repo, with commits off, or in dry-run — the edits are
// still applied, and /undo still reaches them through the snapshot.
//
// The error is git refusing the commit — a pre-commit hook, most often — and
// is returned as well as printed, because the commit tool has to tell the model
// why. Returning nothing left it to infer "nothing to commit" from the hash
// not moving, which is the one reading the hook's refusal rules out.
//
// The hashes it makes go on the open snapshot, so /undo can take them back.
func (c *Coder) commitTurn(message string) (turnCommit, error) {
	var tc turnCommit
	if c.Repo == nil || !c.Commits || c.DryRun {
		return tc, nil
	}
	edited := c.committablePaths(c.pendingPaths())
	staged, conflicted := c.stagedForCommit(edited)
	tc.conflicted = conflicted
	if len(conflicted) > 0 {
		c.Out.Toolf("Not committing %s: unresolved merge conflicts.", strings.Join(conflicted, ", "))
	}
	if len(edited) == 0 && len(staged) == 0 {
		return tc, nil
	}
	slices.Sort(edited)

	// The user's uncommitted changes to these files from before the model's
	// work began, first, in a commit of their own.
	if h := c.commitUncommittedFirst(append(slices.Clone(edited), staged...)); h != "" {
		c.noteCommit(h)
	}

	hash, message, ok, err := c.Repo.Commit(edited, staged, message)
	if err != nil {
		// A commit failure leaves the edits in the tree and pending, where
		// the next commit finds them and /undo still reaches them.
		c.Out.Errorf("Could not commit: %v", err)
		return tc, err
	}
	// Edited paths too: one the model staged and then edited went in as an
	// edit, and must not be announced again by the next commit.
	for _, p := range append(slices.Clone(staged), edited...) {
		delete(c.pendingStaged, p)
		delete(c.pendingEdits, p)
	}
	c.closePending()
	if !ok {
		// The writes since the last commit net out against what is
		// committed — a change and its reversal, or a rewrite of what was
		// already there.
		if c.lastCommitHash != "" {
			c.Out.Toolf("Nothing to commit since %s.", c.lastCommitHash)
		} else {
			c.Out.Toolf("The files are as they were; nothing to commit.")
		}
		return tc, nil
	}

	c.noteCommit(hash)
	c.Out.Toolf("Commit %s %s", hash, message)
	tc.staged = staged
	if len(staged) > 0 {
		c.Out.Toolf("Included what the model's commands staged with git: %s.", strings.Join(staged, ", "))
	}
	return tc, nil
}

// pendingPaths is everything the model has written since its last commit:
// what earlier settles closed and what the open snapshot holds.
func (c *Coder) pendingPaths() []string {
	set := maps.Clone(c.pendingEdits)
	if set == nil {
		set = map[string]bool{}
	}
	for _, p := range c.turnSnap.paths() {
		set[p] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// hasPending reports whether there is anything for a commit to take.
func (c *Coder) hasPending() bool {
	return !c.turnSnap.empty() || len(c.pendingEdits) > 0 || len(c.pendingStaged) > 0
}

// noteCommit records a commit the session made: as the newest, and on the
// open snapshot, for /undo.
func (c *Coder) noteCommit(hash string) {
	c.lastCommitHash = hash
	if c.turnSnap == nil {
		c.turnSnap = newTurnSnapshot()
	}
	c.turnSnap.commits = append(c.turnSnap.commits, hash)
}

// turnCommit is what commitTurn put in a commit beyond the turn's own edits.
type turnCommit struct {
	staged     []string // staged by the model's shell commands
	conflicted []string // staged, but left out for unresolved conflicts
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
	for _, h := range slices.Backward(hashes) { // newest first; the snapshot wants oldest first
		c.noteCommit(h)
	}
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

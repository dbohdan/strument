package coder

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/workspace"
)

// Files a turn's commands left behind.
//
// The edit tools' files are in the turn summary and the auto-commit, but a
// file a command created — a compiled binary, a scratch script, a log — is in
// neither, and neither the user nor the model is told it exists. The case that
// found this was a FrontierHarness task asking for one file in a directory:
// Kimi K3 wrote a correct solution, verified it with the task's own
// `gcc … -o /app/polyglot/cmain`, and left the binary beside it, failing the
// test's "only main.py.c" check in seven runs out of seven.
//
// What is reported is narrow on purpose: files that are untracked, not
// ignored, absent when the turn began, and not written by the edit tools. So
// everything on the list is something this turn's commands created, and
// removing one can only undo the turn's own work. A prompt rule asking the
// model to clean up was the alternative, and was turned down because it
// invites removing things that were meant to stay; this states a fact and
// leaves the judgment where it was.
//
// Git projects only: the snapshot is `git ls-files --others --exclude-standard`,
// which knows what is ignored. A session without git gets nothing, rather than
// a directory walk that would have to reinvent .gitignore.

// maxNewFilesListed caps the list. A command that unpacked an archive or
// installed dependencies into an unignored directory creates thousands, and
// the note is for noticing, not for inventory.
const maxNewFilesListed = 20

// snapshotUntracked records the untracked files at the start of a turn. nil
// means there is no snapshot, so nothing is reported for this turn.
func (c *Coder) snapshotUntracked() {
	c.untrackedBefore = nil
	c.newFilesNoted = false
	if c.Repo == nil || c.DryRun {
		return
	}
	files, err := c.Repo.UntrackedFiles()
	if err != nil {
		return
	}
	c.untrackedBefore = make(map[string]bool, len(files))
	for _, f := range files {
		c.untrackedBefore[f] = true
	}
}

// filesCreatedByCommands lists, as display paths and sorted, the untracked
// files that appeared during the turn without passing through the edit tools.
func (c *Coder) filesCreatedByCommands() []string {
	if c.untrackedBefore == nil {
		return nil
	}
	now, err := c.Repo.UntrackedFiles()
	if err != nil {
		return nil
	}
	edited := make(map[string]bool, len(c.turnEditedFiles))
	for f := range c.turnEditedFiles {
		edited[c.absRootPath(f)] = true
	}
	var out []string
	for _, rel := range now {
		if c.untrackedBefore[rel] {
			continue
		}
		abs := workspace.ResolveSymlinks(filepath.Join(c.Repo.Root(), filepath.FromSlash(rel)))
		if edited[abs] {
			continue
		}
		out = append(out, c.displayName(abs))
	}
	slices.Sort(out)
	return out
}

// formatNewFiles renders the list, capped.
func formatNewFiles(files []string, sep string) string {
	if len(files) <= maxNewFilesListed {
		return strings.Join(files, sep)
	}
	return strings.Join(files[:maxNewFilesListed], sep) + sep +
		fmt.Sprintf("and %d more", len(files)-maxNewFilesListed)
}

// noteNewFiles is the end-of-turn observation, made once per turn at the point
// the model believes it is done, the same point the automatic checks run: late
// enough that the files are final, early enough that the model can still act.
//
// The wording states what happened and says that leaving the files is a fine
// answer. A note that read as "clean up" would get build outputs a user wanted
// deleted along with the ones they didn't.
func (c *Coder) noteNewFiles() (message string, keepGoing bool) {
	if c.newFilesNoted {
		return "", false
	}
	files := c.filesCreatedByCommands()
	if len(files) == 0 {
		return "", false
	}
	c.newFilesNoted = true
	c.Out.Toolf("Commands this turn created untracked files: %s.", formatNewFiles(files, ", "))
	return "Strument noticed that commands run during this turn created files that git does not track " +
		"or ignore and that your edits did not write:\n\n- " + formatNewFiles(files, "\n- ") +
		"\n\nThey may be meant to stay. If any is a by-product that should not be left behind, " +
		"remove it; otherwise leave them as they are.", true
}

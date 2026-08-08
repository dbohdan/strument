package coder

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// writePlan is a batch of file writes to apply as one unit: the final content
// of each touched path, plus first-touch order so application is deterministic.
//
// This used to be editblock.PlanResult. Planning moved out of editblock with the
// text formats: with a typed path argument there is nothing to parse and nothing
// to re-attribute, so what remains is just the batch itself.
type writePlan struct {
	Writes     map[string]string
	WriteOrder []string
}

// diskReader reads current file contents for the edit planner.
type diskReader struct{ root string }

func (d diskReader) ReadFile(rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// unsafePath rejects absolute paths, traversal outside the root, and
// symlink escapes — except for files the user explicitly added to the chat,
// which are sanctioned targets wherever they live.
func (c *Coder) unsafePath(rel string) string {
	if rel == "" {
		return "empty path"
	}
	// A file already in the chat was chosen by the user, so it may live
	// outside the project root (a sibling project reached through a symlinked
	// directory, an out-of-tree file added by relative path). The containment
	// boundary below guards against the model inventing new out-of-root
	// paths, not against editing what the user deliberately added.
	if slices.Contains(c.absFnames, c.absRootPath(rel)) {
		return ""
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return "absolute paths are not allowed"
	}
	full := filepath.Clean(filepath.Join(c.Root, filepath.FromSlash(rel)))
	rootAbs, err := filepath.Abs(c.Root)
	if err != nil {
		return "cannot resolve root"
	}
	relBack, err := filepath.Rel(rootAbs, full)
	if err != nil || relBack == ".." || strings.HasPrefix(relBack, ".."+string(filepath.Separator)) {
		return "path escapes the project root"
	}
	// Symlink escape: compare the fully resolved file and root (resolvePath
	// handles not-yet-created files by resolving the deepest existing ancestor).
	rel2, err := filepath.Rel(resolvePath(rootAbs), resolvePath(full))
	if err != nil || rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
		return "path resolves outside the project root (symlink escape)"
	}
	return ""
}

// allowedToEdit decides whether an edit may touch rel, and brings the file into
// the chat when it does.
//
// aider asked before creating a file or editing one the user had not added
// (allowed_to_edit, base_coder.py:2191). Strument no longer does, because the
// model now finds files itself: those prompts moved from the exceptional path to
// the common one, and a confirmation that always appears is not a safety
// feature — it teaches the user to answer yes without reading it. Scoping it to
// once per file or once per turn only slows that training down.
//
// What guards an edit is everything here that is not a question: path
// containment (unsafePath, checked before this), the gitignore refusal below,
// a dirty-commit before the edit so /undo has a clean base, git auto-commit,
// and the diff scrolling past as it happens. Review lives in the diff and in
// being able to undo, not in a y/n the user has learned to dismiss.
func (c *Coder) allowedToEdit(rel string, needDirtyCommit map[string]bool) bool {
	full := c.absRootPath(rel)

	if slices.Contains(c.absFnames, full) {
		c.checkForDirtyCommit(rel, needDirtyCommit)
		return true
	}

	// Still refused, and not as a prompt: an ignored file is one the project
	// declared out of scope, and the observation tools do not show it either.
	if c.Repo != nil && c.Repo.GitIgnored(rel) {
		c.Out.Warningf("Skipping edits to %s that matches gitignore spec.", rel)
		return false
	}

	if _, err := os.Stat(full); err != nil {
		if !c.DryRun {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				c.Out.Errorf("Unable to create %s, skipping edits.", rel)
				return false
			}
		}
		c.absFnames = append(c.absFnames, full)
		return true
	}

	c.absFnames = append(c.absFnames, full)
	c.checkForDirtyCommit(rel, needDirtyCommit)
	return true
}

// checkForDirtyCommit separates the user's uncommitted work from the turn's, by
// committing theirs before the first edit lands on a file.
//
// It must not fire on a file this turn has already written. Once the commit
// moved to turn end, the turn's own first edit leaves the file dirty, so a
// second edit to it looked exactly like the user's uncommitted work — and
// committing there swept the turn's changes into an unattributed commit with no
// trailer, which /undo and /squash then rightly refused to touch. The turn's
// snapshot is the record of what it has written, so it is also the test.
func (c *Coder) checkForDirtyCommit(rel string, needDirtyCommit map[string]bool) {
	if c.Repo == nil || !c.Repo.IsDirty(rel) {
		return
	}
	if c.turnSnap.wrote(rel) {
		return // dirty because of this turn; there is nothing of the user's here
	}
	c.Out.Toolf("Committing %s before applying edits.", rel)
	needDirtyCommit[rel] = true
}

// dirtyCommit commits dirty files before edits so /undo has a clean base.
// These are user changes: no trailer. Files sort for
// deterministic commits.
func (c *Coder) dirtyCommit(need map[string]bool) {
	if c.Repo == nil || len(need) == 0 {
		return
	}
	files := slices.Sorted(maps.Keys(need))
	if _, _, _, err := c.Repo.Commit(files, "", false); err != nil {
		c.Out.Errorf("Unable to commit dirty files: %v", err)
	}
}

// newFileMode is what a file Strument creates gets. It matches what git
// checkout writes; the temp-then-rename below cannot consult the umask
// portably, so this is a constant rather than 0o666 &^ umask.
const newFileMode = 0o644

// fullPath is where a relative path actually lives on disk, with symlinks
// resolved.
//
// Resolving matters because writeAtomically renames a temp file into place, and
// a rename replaces the *link* rather than following it: without this, editing
// a symlinked file silently turns the link into a regular file and leaves the
// real file with its old contents — the edit lands nowhere the user was looking.
// unsafePath has already resolved this same path and required it to stay inside
// the project root (or to be a file the user added deliberately), so following
// the link here agrees with the check that already ran.
func (c *Coder) fullPath(rel string) string {
	return resolvePath(filepath.Join(c.Root, filepath.FromSlash(rel)))
}

// writeAtomically writes the plan's files via temp+rename, rolling the
// batch back on any failure.
//
// The pre-write contents it reads for that rollback are also the turn's
// snapshot, so on success they are handed to recordWrites rather than dropped.
// On failure they are not: a batch that rolled back changed nothing, and
// recording it would give /undo a turn to unwind that never happened.
//
// The temp file takes the mode of the file it replaces. A rename swaps inodes,
// so without that every edit would leave the file at newFileMode whatever it
// was before: scripts would stop being executable and a 0o600 file — an .env, a
// key, an SSH config — would come back world-readable. Changing a file's
// contents is what was asked for; changing who can read or run it was not.
func (c *Coder) writeAtomically(plan writePlan) error {
	backups := map[string]snapEntry{}
	var order []string

	restore := func() {
		for _, rel := range order {
			b := backups[rel]
			full := c.fullPath(rel)
			if b.existed {
				_ = os.WriteFile(full, b.before, b.mode)
			} else {
				_ = os.Remove(full)
			}
		}
	}

	for _, rel := range plan.WriteOrder {
		full := c.fullPath(rel)
		old, err := os.ReadFile(full)
		existed := err == nil
		mode := os.FileMode(newFileMode)
		if existed {
			if fi, err := os.Stat(full); err == nil {
				// Perm plus the setuid/setgid/sticky bits: everything os.Chmod
				// can put back. Strument did not make the file special and has
				// no business making it ordinary.
				mode = fi.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
			}
		}
		backups[rel] = snapEntry{before: old, existed: existed, mode: mode}
		order = append(order, rel)

		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			restore()
			return fmt.Errorf("mkdir for %s: %w", rel, err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(full), ".strument-*")
		if err != nil {
			restore()
			return fmt.Errorf("temp for %s: %w", rel, err)
		}
		if _, err := tmp.WriteString(plan.Writes[rel]); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			restore()
			return fmt.Errorf("write %s: %w", rel, err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			restore()
			return fmt.Errorf("close %s: %w", rel, err)
		}
		if err := os.Chmod(tmp.Name(), mode); err != nil {
			os.Remove(tmp.Name())
			restore()
			return fmt.Errorf("chmod %s: %w", rel, err)
		}
		if err := os.Rename(tmp.Name(), full); err != nil {
			os.Remove(tmp.Name())
			restore()
			return fmt.Errorf("rename %s: %w", rel, err)
		}
	}
	c.recordWrites(plan, backups)
	return nil
}

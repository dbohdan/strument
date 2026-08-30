package coder

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/workspace"
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
	// The repository's own internals, refused before the pinned exemption for
	// the reason workspace.UnderGitDir gives: a write there is code execution
	// in Strument's own unfiltered git, with no prompt and nothing in the diff.
	// This is the check the read path has always had and this one had not, and
	// it is the same function now rather than a second statement of the rule —
	// the last time these two were kept in step by hand they drifted on the
	// order of the absolute-path test.
	if workspace.UnderGitDir(rel) {
		return "the repository's own .git directory is not project content"
	}
	// A file the user pinned was chosen by the user, so it may live outside the
	// project root (a sibling project reached through a symlinked directory, an
	// out-of-tree file added by relative path). The containment boundary below
	// guards against the model inventing new out-of-root paths, not against
	// editing what the user deliberately added.
	//
	// Read-only pins are exempt here too, and that grants nothing: allowedToEdit
	// refuses every one of them unconditionally. What it buys is the *right
	// refusal*. An out-of-tree reference used to be rejected here first, with
	// "path escapes the project root" — true, but not the reason — and the model
	// never saw the read-only message at all. Live sessions show what that cost:
	// models retried with an absolute path, then through the shell, then went
	// looking for a writable copy of the file inside the project, one of them for
	// twelve steps. Told it is read-only, they say so and move on.
	if c.isPinned(rel) {
		return ""
	}
	// An absolute path the model sends is resolved to its root-relative form
	// and the rest of the checks run on that, the same normalization contain()
	// (workspace) does on the read side — the two are documented as mirrors and
	// had drifted once already, on the order of the absolute-path test. Resolve
	// first, refuse second: the refusal must see the file the kernel would
	// open, not the string the caller spelled. Small models habitually send
	// absolute paths (Maple-Preview by DeepGrove was the first observed), and
	// refusing the spelling cost a round trip that taught nothing when the file
	// was in scope either way.
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		rootAbs, err := filepath.Abs(c.Root)
		if err != nil {
			return "cannot resolve root"
		}
		resolved := workspace.ResolveSymlinks(filepath.Clean(filepath.FromSlash(rel)))
		relBack, err := filepath.Rel(rootAbs, resolved)
		if err != nil || workspace.EscapesRoot(relBack) {
			return "path escapes the project root"
		}
		// The resolved form is checked too, not only the caller's spelling: a
		// path that resolves to .git/config through a link named anything else
		// arrives here without a .git segment in the raw string.
		if workspace.UnderGitDir(relBack) {
			return "the repository's own .git directory is not project content"
		}
		rel = filepath.ToSlash(relBack)
	}
	// The remaining checks run on the root-relative form, whatever it arrived
	// as. They are the ones that were always here, unchanged.
	full := filepath.Clean(filepath.Join(c.Root, filepath.FromSlash(rel)))
	rootAbs, err := filepath.Abs(c.Root)
	if err != nil {
		return "cannot resolve root"
	}
	relBack, err := filepath.Rel(rootAbs, full)
	if err != nil || workspace.EscapesRoot(relBack) {
		return "path escapes the project root"
	}
	// Symlink escape: compare the fully resolved file and root (resolvePath
	// handles not-yet-created files by resolving the deepest existing ancestor).
	rel2, err := filepath.Rel(workspace.ResolveSymlinks(rootAbs), workspace.ResolveSymlinks(full))
	if err != nil || workspace.EscapesRoot(rel2) {
		return "path resolves outside the project root (symlink escape)"
	}
	return ""
}

// normalizeToolPath rewrites an absolute path that resolves inside the root to
// its root-relative form, and leaves everything else alone.
//
// The edit pipeline keys its overlay, its rollback map, and its result lines on
// the path as given, and its disk reader joins it to the root — all of which
// are written for the relative form the tools' own listings use. Rather than
// teach each of those about two spellings, the absolute one is folded away
// here: the model gets its answer in the same namespace every other tool
// result uses, and the write lands on the file it named. Out-of-root absolute
// paths pass through unchanged — they name a pinned file (unsafePath's
// carve-out, and fullPath already keeps those straight) or they will be
// refused.
func (c *Coder) normalizeToolPath(p string) string {
	if !filepath.IsAbs(p) && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "\\") {
		return p
	}
	rootAbs, err := filepath.Abs(c.Root)
	if err != nil {
		return p
	}
	relBack, err := filepath.Rel(rootAbs, workspace.ResolveSymlinks(filepath.Clean(filepath.FromSlash(p))))
	if err != nil || workspace.EscapesRoot(relBack) {
		return p
	}
	return filepath.ToSlash(relBack)
}

// allowedToEdit decides whether an edit may touch rel, and brings the file into
// the chat when it does. The second return is a model-facing reason on refusal,
// "" otherwise: a call that is skipped answers with why, so the model can act on
// it within the turn instead of re-trying the same thing.
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
func (c *Coder) allowedToEdit(rel string, needDirtyCommit map[string]bool) (bool, string) {
	full := c.absRootPath(rel)

	// Read-only first, so it wins over a file that is in both lists. This is
	// checked here rather than left to the prompt because the prompt was all
	// there was: /read-only asked the model not to edit and nothing enforced
	// it, so the file fell through below and was quietly promoted into the
	// editable set. A reference the user reached outside the project for is the
	// worst thing to edit by accident — it is outside the repo, outside the
	// diff they are watching, and outside git's undo.
	if slices.Contains(c.absReadOnlyFnames, full) {
		// The user-facing warning uses the interface's vocabulary ("pinned
		// read-only"); the tool result below keeps the prompt's, which the
		// read-only live trial settled (see prompts.readOnlyFilesPrefix). The two
		// registers do not have to match — nobody reads both.
		c.Out.Warningf("Skipping edits to %s, which is pinned read-only.", rel)
		return false, "that file is pinned as read-only reference, so it was not changed. " +
			"Make the change elsewhere, or ask the user to add it with /add if it should be editable."
	}

	if slices.Contains(c.absFnames, full) {
		c.checkForDirtyCommit(rel, needDirtyCommit)
		return true, ""
	}

	// Still refused, and not as a prompt: an ignored file is one the project
	// declared out of scope, and the observation tools do not show it either.
	if c.Repo != nil && c.Repo.GitIgnored(rel) {
		c.Out.Warningf("Skipping edits to %s that matches gitignore spec.", rel)
		return false, "that file matches a gitignore pattern, so the project treats it as out of scope."
	}

	if _, err := os.Stat(full); err != nil {
		if !c.DryRun {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				c.Out.Errorf("Unable to create %s, skipping edits.", rel)
				return false, "the parent directory could not be created."
			}
		}
		c.absFnames = append(c.absFnames, full)
		return true, ""
	}

	c.absFnames = append(c.absFnames, full)
	c.checkForDirtyCommit(rel, needDirtyCommit)
	return true, ""
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
	if needDirtyCommit[rel] {
		return // already queued by an earlier edit in this batch
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
	if _, _, _, err := c.Repo.Commit(files, "", "", false); err != nil {
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
	// Absolute already: leave it alone. unsafePath refuses every absolute path
	// except one naming a file the user pinned, so the only way to get here
	// with one is that carve-out -- and joining it onto the root turned
	// "/tmp/p/window.go" into "<root>/tmp/p/window.go". The write then landed
	// in a shadow tree mirroring the whole absolute path inside the project,
	// the real file kept its old contents, and nothing said so.
	//
	// The same guard absRootPath has always had. contain() spells it
	// filepath.Clean(raw) on its side. This is the third time these three have
	// been found disagreeing about absolute paths, and the first time they
	// agreed on the decision and differed on the destination, which is why it
	// was silent rather than a refusal.
	if filepath.IsAbs(rel) {
		return workspace.ResolveSymlinks(filepath.Clean(filepath.FromSlash(rel)))
	}
	return workspace.ResolveSymlinks(filepath.Join(c.Root, filepath.FromSlash(rel)))
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

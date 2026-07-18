package coder

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/editblock"
)

// diskReader reads current file contents for the edit planner.
type diskReader struct{ root string }

func (d diskReader) ReadFile(rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// applyUpdates dispatches on the active edit format (§7.1).
func (c *Coder) applyUpdates(answer string) ([]string, string) {
	switch c.editFormat {
	case "ask":
		// Ask mode's engine parses nothing (aider's base get_edits returns
		// []): no edits, no shell collection, so everything downstream —
		// auto-commit, history rotation, edit-failure reflection — no-ops
		// by construction. A well-formed SEARCH/REPLACE block in an ask
		// answer is left as prose.
		return nil, ""
	case "whole":
		return c.applyWholeFileUpdates(answer)
	default:
		return c.applyEditBlockUpdates(answer)
	}
}

// applyWholeFileUpdates handles the trivial "whole" format: fenced full-file
// listings that overwrite their targets.
func (c *Coder) applyWholeFileUpdates(answer string) ([]string, string) {
	fen := editblock.Fence{Open: c.fence.open, Close: c.fence.close}
	edits, err := editblock.ParseWholeFile(answer, fen, c.inchatRelativeFiles())
	if err != nil {
		c.Out.Errorf("The LLM did not conform to the edit format.")
		c.Out.Printf("%s", err.Error())
		return nil, err.Error()
	}
	if len(edits) == 0 {
		return nil, ""
	}

	var plan editblock.PlanResult
	plan.Writes = map[string]string{}
	needDirtyCommit := map[string]bool{}
	var edited []string
	for _, e := range edits {
		if reason := c.unsafePath(e.Path); reason != "" {
			c.Out.Errorf("Skipping edit to %s: %s", e.Path, reason)
			continue
		}
		if !c.allowedToEdit(e.Path, needDirtyCommit) {
			continue
		}
		plan.Writes[e.Path] = e.Content
		plan.WriteOrder = append(plan.WriteOrder, e.Path)
		edited = append(edited, e.Path)
	}
	c.dirtyCommit(needDirtyCommit)
	if len(edited) == 0 {
		return nil, ""
	}
	if !c.DryRun {
		if err := c.writeAtomically(plan); err != nil {
			c.Out.Errorf("Exception while updating files:")
			c.Out.Errorf("%s", err.Error())
			return nil, ""
		}
	}
	for _, p := range edited {
		if c.DryRun {
			c.Out.Printf("Did not apply edit to %s (--dry-run)", p)
		} else {
			c.Out.Printf("Applied edit to %s", p)
		}
	}
	return edited, ""
}

// applyEditBlockUpdates is the SEARCH/REPLACE pipeline: parse -> reject
// unsafe paths -> plan (dry) -> prepareToEdit confirms -> re-plan allowed ->
// write atomically -> report/reflect. It returns the edited rel paths and a
// reflection message ("" if none).
func (c *Coder) applyEditBlockUpdates(answer string) ([]string, string) {
	blocks, err := editblock.FindBlocks(answer, editblock.Fence{Open: c.fence.open, Close: c.fence.close}, c.inchatRelativeFiles())
	if err != nil {
		c.Out.Errorf("The LLM did not conform to the edit format.")
		c.Out.Printf("%s", err.Error())
		return nil, err.Error()
	}

	var edits []editblock.Edit
	for _, b := range blocks {
		if b.IsShell {
			c.addShellCommand(b.Shell)
			continue
		}
		edits = append(edits, b.Edit)
	}
	if len(edits) == 0 {
		return nil, ""
	}

	// Path containment is the first security boundary: reject before any
	// FS read (§7.1). Unsafe paths are reported, not reflected (§7.2).
	var safe []editblock.Edit
	for _, e := range edits {
		if reason := c.unsafePath(e.Path); reason != "" {
			c.Out.Errorf("Skipping edit to %s: %s", e.Path, reason)
			continue
		}
		safe = append(safe, e)
	}
	if len(safe) == 0 {
		return nil, ""
	}

	reader := diskReader{root: c.Root}
	chatFiles := c.inchatRelativeFiles()
	fen := editblock.Fence{Open: c.fence.open, Close: c.fence.close}

	// Dry plan to learn target paths (including cross-file reattribution).
	plan := editblock.ApplyEdits(safe, chatFiles, reader, fen)

	// prepareToEdit: per-path permission (create-new / not-in-chat) and the
	// dirty-commit contract.
	allowedPath := map[string]bool{}
	decided := map[string]bool{}
	needDirtyCommit := map[string]bool{}
	decide := func(path string) bool {
		if v, ok := decided[path]; ok {
			return v
		}
		v := c.allowedToEdit(path, needDirtyCommit)
		decided[path] = v
		if v {
			allowedPath[path] = true
		}
		return v
	}
	var allowed []editblock.Edit
	for _, e := range append(append([]editblock.Edit(nil), plan.Applied...), plan.Failed...) {
		if decide(e.Path) {
			allowed = append(allowed, e)
		}
	}
	if len(allowed) == 0 {
		return nil, ""
	}
	c.dirtyCommit(needDirtyCommit)

	// Re-plan over the allowed set (overlay composition can change when
	// edits were filtered) and write.
	plan = editblock.ApplyEdits(allowed, chatFiles, reader, fen)

	if !c.DryRun {
		if err := c.writeAtomically(plan); err != nil {
			c.Out.Errorf("Exception while updating files:")
			c.Out.Errorf("%s", err.Error())
			return nil, ""
		}
	}

	// edited = allowed target paths (aider counts allowed-but-failed paths
	// too; they auto-commit as no-ops).
	editedSet := map[string]bool{}
	var edited []string
	for _, e := range allowed {
		if !editedSet[e.Path] {
			editedSet[e.Path] = true
			edited = append(edited, e.Path)
		}
	}

	for _, p := range edited {
		if c.DryRun {
			c.Out.Printf("Did not apply edit to %s (--dry-run)", p)
		} else {
			c.Out.Printf("Applied edit to %s", p)
		}
	}

	if plan.Report != "" {
		c.Out.Errorf("The LLM did not conform to the edit format.")
		c.Out.Printf("%s", plan.Report)
		return edited, plan.Report
	}
	return edited, ""
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
	// Symlink escape: resolve the deepest existing ancestor.
	dir := filepath.Dir(full)
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			rootResolved, rerr := filepath.EvalSymlinks(rootAbs)
			if rerr != nil {
				rootResolved = rootAbs
			}
			rel2, err2 := filepath.Rel(rootResolved, resolved)
			if err2 != nil || rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
				return "path resolves outside the project root (symlink escape)"
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// allowedToEdit ports allowed_to_edit (base_coder.py:2191): chat files are
// allowed (with a dirty-commit check); new files and out-of-chat files need
// confirmation and join the chat.
func (c *Coder) allowedToEdit(rel string, needDirtyCommit map[string]bool) bool {
	full := c.absRootPath(rel)

	inChat := slices.Contains(c.absFnames, full)
	if inChat {
		c.checkForDirtyCommit(rel, needDirtyCommit)
		return true
	}

	if c.Repo != nil && c.Repo.GitIgnored(rel) {
		c.Out.Warningf("Skipping edits to %s that matches gitignore spec.", rel)
		return false
	}

	if _, err := os.Stat(full); err != nil {
		yes, _ := c.Confirm.Confirm(ConfirmRequest{Prompt: "Create new file?", Subject: rel})
		if !yes {
			c.Out.Printf("Skipping edits to %s", rel)
			return false
		}
		if !c.DryRun {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				c.Out.Errorf("Unable to create %s, skipping edits.", rel)
				return false
			}
		}
		c.absFnames = append(c.absFnames, full)
		return true
	}

	yes, _ := c.Confirm.Confirm(ConfirmRequest{
		Prompt:  "Allow edits to file that has not been added to the chat?",
		Subject: rel,
	})
	if !yes {
		c.Out.Printf("Skipping edits to %s", rel)
		return false
	}
	c.absFnames = append(c.absFnames, full)
	c.checkForDirtyCommit(rel, needDirtyCommit)
	return true
}

func (c *Coder) checkForDirtyCommit(rel string, needDirtyCommit map[string]bool) {
	if c.Repo == nil || !c.Repo.IsDirty(rel) {
		return
	}
	c.Out.Printf("Committing %s before applying edits.", rel)
	needDirtyCommit[rel] = true
}

// dirtyCommit commits dirty files before edits so /undo has a clean base
// (§7.3 contract). These are user changes: no trailer. Files sort for
// deterministic commits (§3.3).
func (c *Coder) dirtyCommit(need map[string]bool) {
	if c.Repo == nil || len(need) == 0 {
		return
	}
	files := slices.Sorted(maps.Keys(need))
	if _, _, _, err := c.Repo.Commit(files, "", false); err != nil {
		c.Out.Errorf("Unable to commit dirty files: %v", err)
	}
}

// writeAtomically writes the plan's files via temp+rename, rolling the
// batch back on any failure (§7.1).
func (c *Coder) writeAtomically(plan editblock.PlanResult) error {
	type backup struct {
		path    string
		content []byte
		existed bool
	}
	var backups []backup
	var written []string

	restore := func() {
		for _, b := range backups {
			full := filepath.Join(c.Root, filepath.FromSlash(b.path))
			if b.existed {
				_ = os.WriteFile(full, b.content, 0o644) //nolint:gosec // Restoring a project file; sources are world-readable.
			} else {
				_ = os.Remove(full)
			}
		}
	}

	for _, rel := range plan.WriteOrder {
		full := filepath.Join(c.Root, filepath.FromSlash(rel))
		old, err := os.ReadFile(full)
		existed := err == nil
		backups = append(backups, backup{rel, old, existed})

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
		if err := os.Chmod(tmp.Name(), 0o644); err != nil {
			os.Remove(tmp.Name())
			restore()
			return fmt.Errorf("chmod %s: %w", rel, err)
		}
		if err := os.Rename(tmp.Name(), full); err != nil {
			os.Remove(tmp.Name())
			restore()
			return fmt.Errorf("rename %s: %w", rel, err)
		}
		written = append(written, rel)
	}
	_ = written
	return nil
}

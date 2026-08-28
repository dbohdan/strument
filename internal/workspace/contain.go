package workspace

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/gitignore"
)

// This file is the boundary the observation tools are supposed to have had all
// along. read and ls took a caller-supplied path, normalized it, joined it to
// the root, and opened whatever came out:
//
//	read ../outside.env      → the file's contents
//	read etc-link/hostname   → /etc/hostname, through a symlinked directory
//	ls ..                    → the parent directory
//
// Edits went through unsafePath (coder/apply.go) and were fine; reads had no
// check at all. In a harness where a model's instructions can come from a
// README or a scraped page, that is the widest surface there is: nothing
// confirms a read, and what it returns goes to the provider and into the
// transcript.
//
// glob and grep were never affected — they walk the tree rather than joining a
// path — which is exactly why the gap in read survived: three of four tools
// behaved, so the fourth looked like it did too.

// contain resolves rel against the root and refuses anything that leaves it.
//
// Both halves matter and they catch different things. The lexical check stops
// "../.." and absolute paths; the symlink check stops a directory inside the
// project that points outside it, which no amount of string cleaning would
// notice. It mirrors unsafePath (coder/apply.go), deliberately: two containment
// rules that drift apart are worse than one that is stated twice.
//
// It takes the caller's path *before* normalization and normalizes it itself,
// because path() trims leading slashes: checked afterwards, "/etc/hostname"
// would arrive as "etc/hostname" and be answered with "no such file" instead of
// the reason. It stayed contained either way, but a refusal that names the
// wrong problem is the failure the /read-only work spent a session removing.
//
// The returns are the absolute path, the normalized relative one, and a
// caller-facing reason that is "" when the path is fine — the same shape the
// tool layer uses, because these become sentences a model reads.
func (w *Workspace) contain(raw string) (full, rel, reason string) {
	// First, and ahead of the pinned exemption below rather than after it.
	// .git is machine state at any depth and however the caller spells it, and
	// the exemption is not the pure record of user intent it reads as: the edit
	// path appends to absFnames as it goes (apply.go), so a list the model can
	// grow must not be able to unlock this. The user has /run for the rare
	// legitimate look at their own repository's internals.
	if UnderGitDir(raw) {
		return "", "", gitDirRefusal
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) {
		// A pinned file may be named absolutely, which is the carve-out
		// unsafePath has always made and this side did not. The two are
		// documented as mirrors, and they had drifted on exactly this case: the
		// same absolute path for a pinned file was accepted for editing and
		// refused for reading. The order is what differed — unsafePath asks
		// "pinned?" before "absolute?", and this asked the other way round, so
		// the exemption below was unreachable for an absolute path.
		//
		// It matters beyond consistency now that DisplayPath names an
		// out-of-tree pin absolutely: that is the string the prompt hands the
		// model, so it has to be a string the model can read back.
		//
		// Nothing widens here. The exemption is exactly the set of files the
		// user pinned, and Pinned cannot answer true for anything else.
		full = filepath.Clean(raw)
		if w.Pinned != nil && w.Pinned(full) {
			return full, filepath.ToSlash(full), ""
		}
		return "", "", "absolute paths are not allowed; give a path relative to the project root"
	}
	rel = path(raw)
	if rel == "" {
		return w.Root, "", ""
	}
	full = filepath.Clean(filepath.Join(w.Root, filepath.FromSlash(rel)))

	// A pinned file is one the user reached for deliberately, so it is
	// sanctioned wherever it lives — the same carve-out unsafePath makes for
	// edits. The boundary guards against a model inventing a path out of the
	// project, not against what the user put in front of it.
	if w.Pinned != nil && w.Pinned(full) {
		return full, rel, ""
	}

	rootAbs, err := filepath.Abs(w.Root)
	if err != nil {
		return "", "", "the project root cannot be resolved"
	}
	if escapes(rootAbs, full) {
		return "", "", "that path is outside the project root"
	}
	if escapes(resolvePath(rootAbs), resolvePath(full)) {
		return "", "", "that path resolves outside the project root through a symlink"
	}
	return full, rel, ""
}

// escapes reports whether full lies outside root.
func escapes(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvePath follows symlinks as far as the path exists, so a path naming a
// file that is not there yet still resolves through the directories that are.
// Copied from coder rather than imported: workspace is the lower layer and must
// not depend on the one above it.
func resolvePath(abs string) string {
	rest := ""
	dir := abs
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs // reached the root without resolving anything
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// matcherFor builds the ignore matcher in scope for a directory, walking the
// chain of .gitignore files from the root down so a nested one still applies.
// Patterns accumulate in ascending priority, which is git's own order.
func (w *Workspace) matcherFor(domain []string) (gitignore.Matcher, error) {
	patterns, err := gitignore.ReadRoot(w.Root)
	if err != nil {
		return nil, err
	}
	cur := w.Root
	for i, seg := range domain {
		cur = filepath.Join(cur, seg)
		own, err := gitignore.ReadDir(cur, domain[:i+1])
		if err != nil {
			return nil, err
		}
		patterns = slices.Concat(patterns, own)
	}
	return gitignore.NewMatcher(patterns), nil
}

// ignored reports whether the project's ignore rules cover rel.
//
// It exists because read did not consult them, while ls, glob, and grep all
// did — so a gitignored .env was invisible to every way of *finding* it and one
// guessed filename away from being read. README.md said the lookup tools "never
// see a file the project ignores", which was true of three of the four.
//
// Every prefix is tested, not just the whole path, because git prunes at the
// directory: with node_modules/ ignored, node_modules/x/y.js is ignored too
// even though no rule names it. That costs the depth of the path rather than
// the size of the tree.
func (w *Workspace) ignored(rel string, isDir bool) (bool, error) {
	rel = path(rel)
	if rel == "" {
		return false, nil
	}
	components := strings.Split(rel, "/")
	for i := range components {
		matcher, err := w.matcherFor(components[:i])
		if err != nil {
			return false, err
		}
		// Everything but the last component is a directory by construction.
		last := i == len(components)-1
		if matcher.Match(components[:i+1], !last || isDir) {
			return true, nil
		}
	}
	return false, nil
}

// refuseIgnored is the message a caller gives back for an ignored path. A
// pinned file is exempt here as well: the user naming a file is a stronger
// signal than the project's blanket rule, and /add already accepts one.
func (w *Workspace) refuseIgnored(rel, full string) error {
	if w.Pinned != nil && w.Pinned(full) {
		return nil
	}
	yes, err := w.ignored(rel, false)
	if err != nil {
		return err
	}
	if yes {
		return fmt.Errorf("%s is ignored by the project, so it is out of scope", rel)
	}
	return nil
}

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
// "../.." and absolute paths that leave the root; the symlink check stops a
// directory inside the project that points outside it, which no amount of
// string cleaning would notice. It mirrors unsafePath (coder/apply.go), deliberately: two containment
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
	// the exemption must stay a record of user intent (/add, /read-only). The
	// user has /run for the rare legitimate look at their own repository's
	// internals.
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
		// An absolute path that lands inside the root names the same file its
		// relative form does, so it is answered the same. Small models
		// (Maple-Preview by DeepGrove was the first observed) habitually send
		// absolute paths, and a refusal here cost a round trip that taught
		// nothing: the file was in scope either way. The checks that follow are
		// the ones the relative path gets, applied to the file the kernel will
		// actually see — resolve first, refuse second, never the other order.
		rootAbs, err := filepath.Abs(w.Root)
		if err != nil {
			return "", "", "the project root cannot be resolved"
		}
		relBack, err := filepath.Rel(rootAbs, full)
		if err != nil || EscapesRoot(relBack) {
			return "", "", "that path is outside the project root"
		}
		// The resolved form is checked too, not only the caller's spelling:
		// /proj/.gitlink/config resolving to /proj/.git/config must be refused
		// for what it opens, and /etc/passwd was already refused above as
		// out-of-root before this could be reached.
		if UnderGitDir(relBack) {
			return "", "", gitDirRefusal
		}
		if escapes(ResolveSymlinks(rootAbs), ResolveSymlinks(full)) {
			return "", "", "that path resolves outside the project root through a symlink"
		}
		return full, filepath.ToSlash(relBack), ""
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
	if escapes(ResolveSymlinks(rootAbs), ResolveSymlinks(full)) {
		return "", "", "that path resolves outside the project root through a symlink"
	}
	return full, rel, ""
}

// UnderTempDir reports whether abs lies under the platform's standard
// temporary directory — os.TempDir() (TMPDIR on Unix, TMP/TEMP on Windows),
// plus /tmp on Unix, mirroring sandbox.tempDirs: plenty of tools write to
// /tmp regardless of TMPDIR, and os.TempDir is the answer every Go program
// gives to the same question.
//
// Exported because two boundaries ask it: the sandbox, which already grants
// temp writes to model-run commands, and the edit path, which now sanctions
// the same ground for edit/write so a model preparing scratch files for a
// build meets the same boundary on both routes. Both sides compare resolved
// paths — a symlinked root's spelling must not decide containment.
func UnderTempDir(abs string) bool {
	resolved := ResolveSymlinks(filepath.Clean(abs))
	dirs := []string{os.TempDir()}
	if runtime.GOOS != "windows" && os.TempDir() != "/tmp" {
		dirs = append(dirs, "/tmp")
	}
	for _, dir := range dirs {
		rel, err := filepath.Rel(ResolveSymlinks(dir), resolved)
		if err == nil && !EscapesRoot(rel) {
			return true
		}
	}
	return false
}

// PathInRoot reports whether a root-relative path names a file inside the
// named repository root. It is the containment test the commit path needs:
// the turn snapshot keys on the path as the model spelled it, and a path that
// does not live under the repo (a temp-dir scratch file, an out-of-tree pin)
// cannot be handed to git.
func PathInRoot(root, rel string) bool {
	if root == "" {
		return false
	}
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	inside, err := filepath.Rel(root, full)
	return err == nil && !EscapesRoot(inside)
}

// EscapesRoot reports whether a path leaves the root it was made relative to.
// It takes what filepath.Rel returned, not the pair, because every caller has
// already computed that for its own use. The exact test matters: a bare
// HasPrefix(rel, "..") also catches a file honestly named "..notes", and three
// places in the REPL used to do exactly that.
//
// Exported because containment is decided in three packages — the observation
// tools here, the edit path in coder, and the CLI's own argument handling — and
// a rule stated three times is a rule that drifts. It already did once, between
// contain and unsafePath, on the order of the absolute-path test.
func EscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// escapes reports whether full lies outside root.
func escapes(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	return err != nil || EscapesRoot(rel)
}

// ResolveSymlinks follows symlinks as far as the path exists, so a path naming
// a file that is not there yet still resolves through the directories that are.
// On failure it returns abs unchanged.
//
// The lowest of the three layers that need it owns it. It lived here, in coder,
// and in cmd/strument as three byte-identical copies, each with a comment
// explaining that the copy was necessary to avoid depending downward — but
// coder already imports this package, and the CLI already imports thirteen
// internals. Three copies of the primitive both containment rules are built on
// is three chances to fix one and miss two.
func ResolveSymlinks(abs string) string {
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

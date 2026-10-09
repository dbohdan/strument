// Package gitrepo implements the coder's git port by shelling out to the
// git binary — always argv, never a shell string.
// Author and committer identity are left alone: attribution is a single
// sanitized "Assisted-by: <model> via Strument" trailer on auto-commits.
package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repo is a discovered git repository.
type Repo struct {
	root string

	// Trailer is appended to attributed commits via `git commit
	// --trailer`; build it with Trailer() so the model name is sanitized.
	CommitTrailer string

	// Message generates a commit message from staged diffs and chat
	// context (the side-model call); nil or an empty result falls
	// back to aider's "(no commit message provided)".
	Message func(diffs, context string) string

	// Sign, when non-empty, is the `git commit` signing flag passed
	// through as its own argv: "-S" to sign with the default key, or
	// "-S<keyid>" to pick one (git_sign = true / "keyid"). Empty means
	// unsigned.
	Sign string
}

// pinnedGit is the git executable to run, or "" to look the name up on PATH at
// each call — which is the default, and what every caller but main gets.
//
// Pinning exists because `env_set` can change PATH, and Strument's own git
// invocations are the one subprocess that still inherits the whole environment,
// OPENROUTER_API_KEY included, since the commands here pass a nil Env. Every
// subprocess the *model* causes goes through FilterEnv and never sees the key,
// so this is the one path where redirecting the binary would be worth someone's
// while. A trusted project config can already run arbitrary code through its
// checks; what it should not also get is the process holding the credential.
//
// Pinning before any config is applied closes that without touching git's
// environment — which is the alternative, and it risks breaking commit signing
// and credential helpers for a narrower gain.
//
// Opt-in rather than automatic, because pinning defeats PATH interposition on
// purpose, and that is a thing tests legitimately do: TestCommitSignFlag shims
// git to capture argv without running gpg. main pins; nothing else does, so
// nothing else changes behavior.
var pinnedGit string

// gitBinary is what to pass to exec.Command.
func gitBinary() string {
	if pinnedGit != "" {
		return pinnedGit
	}
	return "git"
}

// ResolveBinary pins the git executable to whatever PATH names right now.
//
// Called from main before the config is read, so a later PATH change cannot
// move it. A git that cannot be found leaves the name unpinned, so the failure
// stays the one it always was — exec reports "executable file not found in
// $PATH" at the call site, and a PATH that gains git later still works.
//
// Not safe to call concurrently with git commands; it runs once, during
// startup, before there is anything to race with.
func ResolveBinary() {
	if p, err := exec.LookPath("git"); err == nil {
		pinnedGit = p
	}
}

// Discover finds the repository containing dir, or returns an error when
// dir is not inside a git worktree (or git is not installed).
func Discover(dir string) (*Repo, error) {
	//nolint:gosec // gitBinary is the literal "git" or PATH's answer for it, never input.
	out, err := exec.Command(gitBinary(), "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	root := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(out))))
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &Repo{root: root}, nil
}

// Trailer renders the attribution trailer for a model name, stripping
// newlines and control characters so it stays one well-formed trailer.
func Trailer(modelName string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, modelName)
	clean = strings.TrimSpace(clean)
	if clean == "" {
		clean = "unknown-model"
	}
	return "Assisted-by: " + clean + " via Strument"
}

// git runs one git command in the repo and returns its stdout; errors
// carry stderr for diagnostics.
func (r *Repo) git(args ...string) (string, error) {
	cmd := exec.Command(gitBinary(), append([]string{"-C", r.root}, args...)...) //nolint:gosec // Argv-only git invocation, never a shell string.
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

// ok reports whether a git command exits zero (for predicate commands).
func (r *Repo) ok(args ...string) bool {
	_, err := r.git(args...)
	return err == nil
}

// gitNoOutput runs one git command and reports only whether it succeeded —
// for ref updates, whose stdout is nothing worth capturing.
func (r *Repo) gitNoOutput(args ...string) error {
	_, err := r.git(args...)
	return err
}

// Root returns the worktree root.
func (r *Repo) Root() string { return r.root }

// TrackedFiles returns the tracked files, repo-root-relative.
func (r *Repo) TrackedFiles() []string {
	out, err := r.git("ls-files", "-z")
	if err != nil {
		return nil
	}
	var files []string
	for f := range strings.SplitSeq(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// PathInRepo reports whether rel is tracked.
func (r *Repo) PathInRepo(rel string) bool {
	return r.ok("ls-files", "--error-unmatch", "--", rel)
}

// IsDirty reports whether rel has staged or unstaged changes against HEAD
// (untracked files are not dirty, matching GitPython's is_dirty).
func (r *Repo) IsDirty(rel string) bool {
	out, err := r.git("status", "--porcelain", "--untracked-files=no", "--", rel)
	return err == nil && strings.TrimSpace(out) != ""
}

// GitIgnored reports whether rel matches the ignore rules.
func (r *Repo) GitIgnored(rel string) bool {
	return r.ok("check-ignore", "-q", "--", rel)
}

// ExcludeLocally adds a pattern to this checkout's info/exclude, which git
// honors like .gitignore but never commits. Found through --git-path so a
// worktree writes its own. A pattern already there is not added twice.
func (r *Repo) ExcludeLocally(pattern string) error {
	out, err := r.git("rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.root, path)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for line := range strings.SplitSeq(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + pattern + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// HeadSHA returns the full HEAD hash, or "" on an unborn branch.
func (r *Repo) HeadSHA() string {
	out, err := r.git("rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RootCommit returns the repository's single root commit — the one with no
// parents — or "" when there is not exactly one.
//
// It is the only witness Strument has that two directories are the same project
// after one of them was renamed: a root commit survives a rename, a move across
// filesystems, a restore from backup, and being carried to another machine,
// none of which a path or an inode does.
//
// "Not exactly one" is a deliberate refusal rather than a pick. A history built
// by merging two unrelated repositories has several roots, and which one you get
// depends on traversal order — so a project could witness itself differently on
// two runs. An unborn branch has none. Both answer "" and fall back to being
// listed rather than matched.
//
// It is a witness and not an identity: every clone of a repository shares it.
// The caller pairs it with "the recorded path no longer exists", and even then
// only offers.
func (r *Repo) RootCommit() string {
	out, err := r.git("rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return ""
	}
	roots := strings.Fields(out)
	if len(roots) != 1 {
		return ""
	}
	return roots[0]
}

// Commit commits fnames, as they are in the working tree, and staged, as they
// are in the index. attributed adds the attribution trailer; extra are
// further trailers, each "Key: value". ok=false means there was nothing to
// commit. GIT_AUTHOR_* and GIT_COMMITTER_* are never overridden; hooks run
// normally.
//
// staged is for what a model's shell command put in the index: `git mv a b`
// stages a deletion and an addition, and committing only the file the model
// then edited split the rename. A staged path the index still has is
// committed as it is on disk, like an edited file, and as a deletion if it is
// gone from disk; one the index no longer has is committed as a deletion
// whatever is on disk, so `git rm --cached` untracks rather than being
// undone. Disk rather than the index's copy because models stage a rename
// and then change the file through the shell — `git mv`, then `printf >>` —
// without staging again: committing the staged copy left that change out in
// three live runs across two models, while the commit said the file was in.
//
// The commit is built in a temporary index — HEAD, plus exactly these paths
// — so the user's own staged work in other paths stays out of it, which
// `git commit -- paths` cannot do: it also takes a removed path to be an
// error. Hooks still run, since this is still `git commit`; they see
// GIT_INDEX_FILE, as they do under `git commit -- paths`, which builds a
// temporary index of its own.
//
// want is the message to use. Empty means generate one from the staged diff
// through the Message hook, which is the automatic path; a non-empty one is
// used verbatim, for the commit tool where the model writes its own.
func (r *Repo) Commit(fnames, staged []string, context, want string, attributed bool, extra []string) (hash, message string, ok bool, err error) {
	if len(fnames) == 0 && len(staged) == 0 {
		return "", "", false, nil
	}

	if len(fnames) > 0 {
		addArgs := append([]string{"add", "--"}, fnames...)
		if _, err := r.git(addArgs...); err != nil {
			return "", "", false, fmt.Errorf("could not stage the files: %w", err)
		}
	}
	paths := slices.Sorted(slices.Values(append(slices.Clone(fnames), staged...)))
	paths = slices.Compact(paths)

	idx, cleanup, err := r.commitIndex(paths)
	if err != nil {
		return "", "", false, err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if err := r.stagedFromDisk(env, staged); err != nil {
		return "", "", false, err
	}

	tree, err := r.gitEnv(env, "", "write-tree")
	if err != nil {
		return "", "", false, err
	}
	headTree, herr := r.git("rev-parse", "--verify", "-q", "HEAD^{tree}")
	if herr == nil && strings.TrimSpace(tree) == strings.TrimSpace(headTree) {
		return "", "", false, nil
	}
	if herr != nil {
		// No HEAD yet: nothing to commit if the index holds nothing.
		if out, _ := r.gitEnv(env, "", "ls-files"); strings.TrimSpace(out) == "" {
			return "", "", false, nil
		}
	}

	// A message the caller supplied wins; generating one is what happens when
	// nobody wrote it. The commit tool writes its own, and the model that made
	// the change is better placed to say why than a side model reading the
	// diff afterwards.
	message = strings.TrimSpace(want)
	if message == "" && r.Message != nil {
		diffs, _ := r.gitEnv(env, "", "diff", "--cached")
		message = strings.TrimSpace(r.Message(diffs, context))
		// Models love to quote one-liners (aider strips this too).
		if len(message) >= 2 && message[0] == '"' && message[len(message)-1] == '"' {
			message = strings.TrimSpace(message[1 : len(message)-1])
		}
	}
	if message == "" {
		message = "(no commit message provided)"
	}

	commitArgs := []string{"commit"}
	if r.Sign != "" {
		commitArgs = append(commitArgs, r.Sign)
	}
	commitArgs = append(commitArgs, "-m", message)
	if attributed && r.CommitTrailer != "" {
		commitArgs = append(commitArgs, "--trailer", r.CommitTrailer)
	}
	for _, t := range extra {
		commitArgs = append(commitArgs, "--trailer", t)
	}
	if _, err := r.gitEnv(env, "", commitArgs...); err != nil {
		return "", "", false, err
	}

	// The real index already holds these paths as committed, unless a hook
	// changed one in the temporary index — a formatter that re-adds what it
	// formats. Matching them to HEAD keeps such a path from showing as staged
	// in reverse. Best effort: the commit is made either way.
	_, _ = r.git(append([]string{"reset", "-q", "HEAD", "--"}, paths...)...)

	short, err := r.git("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", "", false, err
	}
	return strings.TrimSpace(short), message, true, nil
}

// stagedFromDisk updates, in the commit's index, each staged path the real
// index still has to what is on disk: the file's contents, or its deletion.
// A submodule keeps its staged commit; disk has a directory there, and
// adding it would stage whatever the submodule's HEAD is now.
func (r *Repo) stagedFromDisk(env, staged []string) error {
	if len(staged) == 0 {
		return nil
	}
	entries, err := r.indexEntries(staged)
	if err != nil {
		return err
	}
	var add, gone []string
	for _, p := range staged {
		es, ok := entries[p]
		if !ok || es[0].mode == "160000" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(r.root, p)); err == nil {
			add = append(add, p)
		} else {
			gone = append(gone, p)
		}
	}
	if len(add) > 0 {
		if _, err := r.gitEnv(env, "", append([]string{"add", "--"}, add...)...); err != nil {
			return fmt.Errorf("could not stage the files: %w", err)
		}
	}
	if len(gone) > 0 {
		if _, err := r.gitEnv(env, "", append([]string{"update-index", "--force-remove", "--"}, gone...)...); err != nil {
			return err
		}
	}
	return nil
}

// commitIndex writes a temporary index holding HEAD's tree with paths as the
// real index has them: a path the real index lacks is removed. It lives in
// the git directory, so a worktree gets its own, and cleanup deletes it.
func (r *Repo) commitIndex(paths []string) (string, func(), error) {
	gitDir, err := r.git("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp(strings.TrimSpace(gitDir), "strument-index-*")
	if err != nil {
		return "", func() {}, err
	}
	idx := f.Name()
	_ = f.Close()
	// An empty file is not a valid index; git writes a fresh one at this name.
	_ = os.Remove(idx)
	cleanup := func() {
		_ = os.Remove(idx)
		_ = os.Remove(idx + ".lock")
	}
	env := []string{"GIT_INDEX_FILE=" + idx}

	if r.ok("rev-parse", "--verify", "-q", "HEAD") {
		_, err = r.gitEnv(env, "", "read-tree", "HEAD")
	} else {
		_, err = r.gitEnv(env, "", "read-tree", "--empty")
	}
	if err != nil {
		cleanup()
		return "", func() {}, err
	}

	entries, err := r.indexEntries(paths)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	var info strings.Builder
	var gone []string
	for _, p := range paths {
		lines, ok := entries[p]
		if !ok {
			gone = append(gone, p)
			continue
		}
		for _, l := range lines {
			if l.stage != "0" {
				cleanup()
				return "", func() {}, fmt.Errorf("%s has unresolved merge conflicts", p)
			}
			info.WriteString(l.mode + " " + l.blob + " " + l.stage + "\t" + p + "\x00")
		}
	}
	if info.Len() > 0 {
		if _, err := r.gitEnv(env, info.String(), "update-index", "-z", "--index-info"); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	if len(gone) > 0 {
		if _, err := r.gitEnv(env, "", append([]string{"update-index", "--force-remove", "--"}, gone...)...); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return idx, cleanup, nil
}

// indexEntry is one line of `git ls-files -s`.
type indexEntry struct{ mode, blob, stage string }

// indexEntries reads the real index, limited to paths when any are given.
// A conflicted path has one entry per stage.
func (r *Repo) indexEntries(paths []string) (map[string][]indexEntry, error) {
	args := []string{"ls-files", "-s", "-z"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := r.git(args...)
	if err != nil {
		return nil, err
	}
	m := map[string][]indexEntry{}
	for rec := range strings.SplitSeq(out, "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		m[path] = append(m[path], indexEntry{mode: f[0], blob: f[1], stage: f[2]})
	}
	return m, nil
}

// MarkIndex records the index as it is now, for IndexChangedSince to compare
// against, and returns the record's path; "" means there was no index to
// record. DropMark deletes it.
//
// The record is a hard link, so taking one costs a syscall rather than a
// listing. That works because git never writes the index in place: it writes
// index.lock and renames it over, which leaves the old file alive under the
// link. Listing the whole index around every model command instead cost 0.4 s
// per command at 100,000 files and 1.3 s at 300,000, most of it parsing. A
// filesystem without hard links gets a copy.
func (r *Repo) MarkIndex() (string, error) {
	idx, gitDir, err := r.indexPath()
	if err != nil {
		return "", err
	}
	r.sweepMarks(gitDir)
	if _, err := os.Stat(idx); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	f, err := os.CreateTemp(gitDir, markPrefix+"*")
	if err != nil {
		return "", err
	}
	mark := f.Name()
	_ = f.Close()
	_ = os.Remove(mark)
	if err := os.Link(idx, mark); err == nil {
		return mark, nil
	}
	data, err := os.ReadFile(idx)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(mark, data, 0o600); err != nil { //nolint:gosec // mark is os.CreateTemp's name in the git directory, not input.
		return "", err
	}
	return mark, nil
}

// markPrefix names index marks in the git directory.
const markPrefix = "strument-index-mark-"

// sweepMarks deletes marks a process left behind by dying before DropMark. A
// day is far longer than any turn a mark lives for.
func (r *Repo) sweepMarks(gitDir string) {
	old, _ := filepath.Glob(filepath.Join(gitDir, markPrefix+"*"))
	for _, m := range old {
		if st, err := os.Stat(m); err == nil && time.Since(st.ModTime()) > 24*time.Hour {
			_ = os.Remove(m)
		}
	}
}

// DropMark deletes a mark MarkIndex made.
func (r *Repo) DropMark(mark string) {
	if mark != "" {
		_ = os.Remove(mark)
	}
}

// IndexChangedSince lists the paths whose index entries — mode, blob, stage —
// differ from the mark's. The same file, or the same bytes, means nothing
// changed and costs no listing; a `git status` that only refreshed stat data
// rewrites the file and costs two, which then compare equal.
func (r *Repo) IndexChangedSince(mark string) ([]string, error) {
	idx, _, err := r.indexPath()
	if err != nil {
		return nil, err
	}
	_, curErr := os.Stat(idx)
	curExists := curErr == nil
	switch {
	case mark == "" && !curExists:
		return nil, nil
	case mark != "" && curExists:
		ms, err1 := os.Stat(mark)
		cs, err2 := os.Stat(idx)
		if err1 == nil && err2 == nil && os.SameFile(ms, cs) {
			return nil, nil
		}
		a, err1 := os.ReadFile(mark)
		b, err2 := os.ReadFile(idx)
		if err1 == nil && err2 == nil && bytes.Equal(a, b) {
			return nil, nil
		}
	}
	list := func(file string) (string, error) {
		if file == "" {
			return "", nil
		}
		return r.gitEnv([]string{"GIT_INDEX_FILE=" + file}, "", "ls-files", "-s", "-z")
	}
	before, err := list(mark)
	if err != nil {
		return nil, err
	}
	var after string
	if curExists {
		if after, err = list(idx); err != nil {
			return nil, err
		}
	}
	if before == after {
		return nil, nil
	}
	return diffListings(before, after), nil
}

// diffListings compares two `git ls-files -s -z` outputs and returns the
// paths whose entries differ. Both are in index order — sorted by path bytes,
// a conflicted path's stages together — so one merge walk does it without a
// map: at 300,000 files, building maps for both sides cost more than the two
// listings.
func diffListings(before, after string) []string {
	b, a := strings.Split(before, "\x00"), strings.Split(after, "\x00")
	path := func(rec string) string {
		_, p, _ := strings.Cut(rec, "\t")
		return p
	}
	var changed []string
	add := func(p string) {
		if p != "" && (len(changed) == 0 || changed[len(changed)-1] != p) {
			changed = append(changed, p)
		}
	}
	i, j := 0, 0
	for i < len(b) || j < len(a) {
		var pb, pa string
		if i < len(b) {
			pb = path(b[i])
		}
		if j < len(a) {
			pa = path(a[j])
		}
		switch {
		case j >= len(a) || (i < len(b) && pb < pa):
			add(pb)
			i++
		case i >= len(b) || pa < pb:
			add(pa)
			j++
		default:
			if b[i] != a[j] {
				add(pb)
			}
			i++
			j++
		}
	}
	slices.Sort(changed)
	return slices.Compact(changed)
}

// lsRecords maps `git ls-files -s -z` output to path → its entries, joined.
func lsRecords(out string) map[string]string {
	m := map[string]string{}
	for rec := range strings.SplitSeq(out, "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if ok {
			m[path] += meta + ";"
		}
	}
	return m
}

// indexPath is the index file and the git directory, both absolute.
func (r *Repo) indexPath() (idx, gitDir string, err error) {
	out, err := r.git("rev-parse", "--absolute-git-dir", "--git-path", "index")
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("unexpected rev-parse output %q", out)
	}
	gitDir, idx = lines[0], lines[1]
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(r.root, idx)
	}
	return idx, gitDir, nil
}

// ConflictedPaths lists which of paths have unresolved merge conflicts.
func (r *Repo) ConflictedPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := r.git(append([]string{"ls-files", "-u", "-z", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	var conflicted []string
	for p := range lsRecords(out) {
		if slices.Contains(paths, p) {
			conflicted = append(conflicted, p)
		}
	}
	slices.Sort(conflicted)
	return conflicted, nil
}

// DirtyPaths lists the tracked paths with staged or unstaged changes against
// HEAD, as IsDirty would report them one at a time. A rename is listed as
// both of its paths.
func (r *Repo) DirtyPaths() (map[string]bool, error) {
	out, err := r.git("status", "--porcelain=v1", "-z", "--untracked-files=no", "--no-renames")
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for rec := range strings.SplitSeq(out, "\x00") {
		if len(rec) > 3 {
			m[rec[3:]] = true
		}
	}
	return m, nil
}

// StagedChanges lists which of paths the index holds differently from HEAD —
// what a commit of them from the index would change. Without a HEAD, that is
// every one of them the index has.
func (r *Repo) StagedChanges(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var out string
	var err error
	if r.ok("rev-parse", "--verify", "-q", "HEAD") {
		out, err = r.git(append([]string{"diff-index", "--cached", "--no-renames", "--name-only", "-z", "HEAD", "--"}, paths...)...)
	} else {
		out, err = r.git(append([]string{"ls-files", "-z", "--"}, paths...)...)
	}
	if err != nil {
		return nil, err
	}
	var changed []string
	for p := range strings.SplitSeq(out, "\x00") {
		if p != "" && slices.Contains(paths, p) {
			changed = append(changed, p)
		}
	}
	return changed, nil
}

// gitEnv is git with extra environment and, when stdin is not empty, input.
func (r *Repo) gitEnv(env []string, stdin string, args ...string) (string, error) {
	cmd := exec.Command(gitBinary(), append([]string{"-C", r.root}, args...)...) //nolint:gosec // Argv-only git invocation, never a shell string.
	cmd.Env = append(os.Environ(), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

// HeadInfo describes HEAD for /undo: full and short hashes, the subject
// line, and the parent count.
func (r *Repo) HeadInfo() (sha, short, subject string, parents int, err error) {
	out, err := r.git("log", "-1", "--format=%H%x00%h%x00%s%x00%P")
	if err != nil {
		return "", "", "", 0, err
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 4)
	if len(parts) != 4 {
		return "", "", "", 0, fmt.Errorf("unexpected git log output %q", out)
	}
	parentList := strings.Fields(parts[3])
	return parts[0], parts[1], parts[2], len(parentList), nil
}

// ChangedInHead lists the files HEAD changed relative to its first parent.
func (r *Repo) ChangedInHead() ([]string, error) {
	out, err := r.git("diff", "--name-only", "-z", "HEAD^", "HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// ChangedInRange lists the files changed between rev and HEAD — what /squash
// needs to re-stage after folding several commits back into the index.
func (r *Repo) ChangedInRange(rev string) ([]string, error) {
	out, err := r.git("diff", "--name-only", "-z", rev, "HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// Commit describes one commit for the /squash gates.
type Commit struct {
	SHA     string
	Short   string
	Subject string
}

// LastCommits returns the n most recent commits, newest first. It returns
// fewer than n only when the branch is shorter than that.
func (r *Repo) LastCommits(n int) ([]Commit, error) {
	out, err := r.git("log", "-n", strconv.Itoa(n), "--format=%H%x00%h%x00%s")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		commits = append(commits, Commit{SHA: parts[0], Short: parts[1], Subject: parts[2]})
	}
	return commits, nil
}

// TrailerValue returns the attribution trailer Commit is appending right now
// (the port's CommitTrailer accessor); it changes when a /model switch
// refreshes the field.
func (r *Repo) TrailerValue() string { return r.CommitTrailer }

// InCommit reports whether rel exists in the given commit's tree.
func (r *Repo) InCommit(commitish, rel string) bool {
	return r.ok("cat-file", "-e", commitish+":"+rel)
}

// AttributeDirectCommits adds the attribution trailer to the commits in
// (fromSHA..HEAD] that a model-created shell command produced and git would
// otherwise record as anonymous — the model ran `git commit` through bash
// instead of the commit tool, so nothing appended a trailer at commit time.
//
// The command may have made several commits, so the whole new chain is
// rewritten, not just the tip: a rewritten commit's descendants need new
// parents. The rewrite is plumbing — read each commit's raw message, append
// the trailer, create a replacement with commit-tree, and move HEAD once at
// the end — so a failure anywhere before that point leaves the original
// history in place, and GIT_AUTHOR_* and GIT_COMMITTER_* stay what the
// command's environment set them to.
//
// Commits are skipped, not rewritten, when they already carry an Assisted-by
// trailer (the model may have committed through the commit tool mid-chain),
// when their author and committer differ (a cherry-pick or revert of someone
// else's work: attributing it to this model would be the one thing a
// provenance trailer must never do), and when the worktree's HEAD is no
// longer a descendant of fromSHA (the command reset or checked out, so the
// commits between are not simply new ones). Merge commits get no trailer —
// a merge is arrangement, not authorship — but still receive rewritten
// parents, so a fork's commits underneath one are not left dangling.
//
// Commits that are not this session's are never touched, and are not
// returned: one reachable from any ref other than the current branch (a
// remote-tracking branch, another local branch, a tag), or committed under an
// identity other than the repository's user.email. A command that ran
// `git pull` fast-forwarded over other people's commits, and author equal to
// committer is exactly what an ordinary teammate commit looks like; without
// this, their commits were rewritten with this model's trailer and new hashes,
// and the branch forked from its remote. The same holds for the model's own
// commit if the command also pushed it: a published commit cannot be
// rewritten without making the next push a force-push, so it goes unattributed
// instead.
//
// Returns the final hashes of this session's commits in the range, newest
// first: the replacement for each rewritten one, the original for each that
// passed through. fromSHA empty or equal to the current HEAD means no new
// commits.
func (r *Repo) AttributeDirectCommits(fromSHA, trailer string) ([]string, error) {
	head, err := r.git("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	head = strings.TrimSpace(head)
	if fromSHA == "" || fromSHA == head {
		return nil, nil
	}
	// The commits to consider are the ones between the tip the coder saw
	// before the command and the tip after it — but only when that range is
	// a strict addition on top, never a replacement of it.
	if !r.ok("merge-base", "--is-ancestor", fromSHA, head) {
		return nil, nil
	}
	out, err := r.git("rev-list", "--reverse", fromSHA+".."+head)
	if err != nil {
		return nil, err
	}
	shas := strings.Fields(out)
	if len(shas) == 0 {
		return nil, nil
	}

	// rawCommit's fields are NUL-delimited to keep messages and identities
	// byte-exact through the round trip.
	const fields = "%T%x00%P%x00%an%x00%ae%x00%aD%x00%cn%x00%ce%x00%cD%x00%B"
	rewrite := map[string]string{} // original SHA -> replacement SHA (or itself)
	notOurs := map[string]bool{}
	branchRef := ""
	if out, err := r.git("symbolic-ref", "--quiet", "HEAD"); err == nil {
		branchRef = strings.TrimSpace(out)
	}
	ourEmail := ""
	if out, err := r.git("config", "--get", "user.email"); err == nil {
		ourEmail = strings.TrimSpace(out)
	}
	for _, sha := range shas {
		out, err := r.git("show", "-s", "--format="+fields, sha)
		if err != nil {
			return nil, err
		}
		f := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 9)
		if len(f) != 9 {
			return nil, fmt.Errorf("unexpected git show output for %s", sha)
		}
		rc := rawCommit{
			tree: f[0], parents: f[1],
			authorName: f[2], authorEmail: f[3], authorDate: f[4],
			committerName: f[5], committerEmail: f[6], committerDate: f[7],
			message: f[8],
		}

		// The replacement chain: each parent that was rewritten points at its
		// replacement; parents before fromSHA (or merge parents from outside
		// the range) pass through as they are.
		var newParents []string
		for p := range strings.FieldsSeq(rc.parents) {
			if rep, ok := rewrite[p]; ok {
				newParents = append(newParents, rep)
			} else {
				newParents = append(newParents, p)
			}
		}

		// A merge is arrangement, not authorship; keep it as-is but with
		// rewritten parents. A commit whose author and committer differ is
		// someone else's work in a new wrapper (cherry-pick, revert); the
		// model arranged it, and the trailer names authors, not arrangers.
		// A commit that already carries an Assisted-by trailer got it from
		// whoever committed it — the commit tool mid-chain, most likely —
		// and a second one would say the model twice.
		isMerge := len(newParents) > 1
		foreign := rc.authorName+"\x00"+rc.authorEmail != rc.committerName+"\x00"+rc.committerEmail
		msg := rc.message
		// Someone else's commit, or one already published: left exactly as it
		// is. Its parents cannot have been rewritten — whatever reaches it
		// reaches them — so keeping its hash keeps the chain intact.
		if r.reachableElsewhere(sha, branchRef) || (ourEmail != "" && rc.committerEmail != ourEmail) {
			notOurs[sha] = true
			rewrite[sha] = sha
			continue
		}
		if !isMerge && !foreign && trailerLine(msg, "Assisted-by") == "" {
			msg = strings.TrimRight(msg, "\n") + "\n\n" + trailer + "\n"
		} else if slices.Equal(newParents, strings.Fields(rc.parents)) {
			// Nothing to add and nothing underneath it moved: the commit is
			// already what it should be. Recreating it would be a new hash
			// for no change wherever commit-tree does not round-trip a
			// message byte for byte.
			rewrite[sha] = sha
			continue
		}
		newSHA, err := r.commitTree(rc, newParents, msg)
		if err != nil {
			return nil, err
		}
		rewrite[sha] = newSHA
	}

	// One ref update at the end: nothing above touched a ref, so any failure
	// left the original history fully in place. The returned hashes are the
	// final chain, newest first.
	if newHead := rewrite[shas[len(shas)-1]]; newHead != head {
		if err := r.moveHead(newHead); err != nil {
			return nil, err
		}
	}
	final := make([]string, 0, len(shas))
	for i := range slices.Backward(shas) {
		if !notOurs[shas[i]] {
			final = append(final, rewrite[shas[i]])
		}
	}
	return final, nil
}

// Published reports whether sha is contained in any remote-tracking branch —
// pushed, under whatever remote or branch name, or pulled from one. Rewriting
// such a commit (squash, undo) makes the next push a force-push.
//
// Any remote and any branch, rather than origin/<current branch>: a remote
// called upstream, or `git push origin HEAD:review`, publishes a commit that
// the narrower check reported as local. An error counts as published, for the
// same reason reachableElsewhere's does.
func (r *Repo) Published(sha string) bool {
	out, err := r.git("for-each-ref", "--contains", sha, "--format=%(refname)", "refs/remotes")
	if err != nil {
		return true
	}
	return strings.TrimSpace(out) != ""
}

// reachableElsewhere reports whether sha is contained in any ref other than
// branchRef: a remote-tracking branch (it was pushed, or pulled), another local
// branch or a tag (it was merged in, or lives on). Either way it is not a
// commit this branch alone holds, which is the only kind that is safe to
// rewrite.
func (r *Repo) reachableElsewhere(sha, branchRef string) bool {
	out, err := r.git("for-each-ref", "--contains", sha, "--format=%(refname)")
	if err != nil {
		// Unknown is treated as elsewhere: the cost is a missing trailer,
		// against rewriting a commit that may be someone else's.
		return true
	}
	for ref := range strings.FieldsSeq(out) {
		if ref != branchRef {
			return true
		}
	}
	return false
}

// commitTree creates a commit object from a parsed commit's tree, parents, and
// message, carrying the original's author and committer identity and dates
// through GIT_AUTHOR_* and GIT_COMMITTER_* — the one place identity must be
// preserved byte-exact, since a rewritten commit that changed the author
// would rewrite provenance while claiming to record it.
func (r *Repo) commitTree(rc rawCommit, parents []string, message string) (string, error) {
	args := make([]string, 0, 2+2*len(parents)+2)
	args = append(args, "commit-tree", rc.tree)
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-m", message)
	//nolint:gosec // Argv-only git invocation, never a shell string.
	cmd := exec.Command(gitBinary(), append([]string{"-C", r.root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+rc.authorName,
		"GIT_AUTHOR_EMAIL="+rc.authorEmail,
		"GIT_AUTHOR_DATE="+rc.authorDate,
		"GIT_COMMITTER_NAME="+rc.committerName,
		"GIT_COMMITTER_EMAIL="+rc.committerEmail,
		"GIT_COMMITTER_DATE="+rc.committerDate,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// trailerLine returns the value of the last trailer-block line with the given
// key, or "" when there is none. It scans only the final paragraph of the
// message: a body paragraph that merely mentions "Assisted-by:" is not a
// trailer, and over-detection only ever skips attribution, never
// misattributes.
func trailerLine(message, key string) string {
	lines := strings.Split(strings.TrimRight(message, "\n"), "\n")
	for i := range slices.Backward(lines) {
		if lines[i] == "" {
			break // the last paragraph before the trailers is the body
		}
		if rest, ok := strings.CutPrefix(lines[i], key+":"); ok && strings.HasPrefix(rest, " ") {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// rawCommit is one commit's parsed plumbing fields, as AttributeDirectCommits
// reads them and commitTree re-creates the object from them.
type rawCommit struct {
	tree, parents                 string
	authorName, authorEmail       string
	authorDate                    string
	committerName, committerEmail string
	committerDate                 string
	message                       string
}

// moveHead points HEAD at sha. A detached HEAD moves by ref update; a branch
// moves by updating the ref it names — the same result as `git reset --soft`,
// without a second command that could fail between the two states.
func (r *Repo) moveHead(sha string) error {
	if out, err := r.git("symbolic-ref", "--quiet", "HEAD"); err == nil {
		return r.gitNoOutput("update-ref", strings.TrimSpace(out), sha)
	}
	return r.gitNoOutput("update-ref", "HEAD", sha)
}

// CurrentBranch returns the checked-out branch name ("" when detached).
func (r *Repo) CurrentBranch() string {
	out, err := r.git("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RevParse resolves a revision to a hash.
func (r *Repo) RevParse(rev string) (string, error) {
	out, err := r.git("rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CheckoutFileFrom restores rel from the given revision into the worktree
// and index.
func (r *Repo) CheckoutFileFrom(rev, rel string) error {
	_, err := r.git("checkout", rev, "--", rel)
	return err
}

// ResetSoft moves HEAD to rev, keeping the index and worktree.
func (r *Repo) ResetSoft(rev string) error {
	_, err := r.git("reset", "--soft", rev)
	return err
}

// DiffWorktree returns `git diff <base>`: the changes from base to the
// current worktree (the /diff view).
func (r *Repo) DiffWorktree(base string) (string, error) {
	return r.git("diff", base)
}

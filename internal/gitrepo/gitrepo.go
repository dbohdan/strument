// Package gitrepo implements the coder's git port by shelling out to the
// git binary — always argv, never a shell string.
// Author and committer identity are left alone: attribution is a single
// sanitized "Assisted-by: <model> via Strument" trailer on auto-commits.
package gitrepo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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

// HeadSHA returns the full HEAD hash, or "" on an unborn branch.
func (r *Repo) HeadSHA() string {
	out, err := r.git("rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Commit stages fnames and commits them. attributed adds the
// trailer (auto-commits of model edits); dirty commits stay unattributed.
// ok=false means there was nothing to commit. GIT_AUTHOR_* and
// GIT_COMMITTER_* are never overridden; hooks run normally.
//
// want is the message to use. Empty means generate one from the staged diff
// through the Message hook, which is the automatic path; a non-empty one is
// used verbatim, for the commit tool where the model writes its own.
func (r *Repo) Commit(fnames []string, context, want string, attributed bool) (hash, message string, ok bool, err error) {
	if len(fnames) == 0 {
		return "", "", false, nil
	}

	addArgs := append([]string{"add", "--"}, fnames...)
	if _, err := r.git(addArgs...); err != nil {
		return "", "", false, fmt.Errorf("unable to add: %w", err)
	}

	statusArgs := append([]string{"status", "--porcelain", "--untracked-files=no", "--"}, fnames...)
	out, err := r.git(statusArgs...)
	if err != nil {
		return "", "", false, err
	}
	if strings.TrimSpace(out) == "" {
		return "", "", false, nil
	}

	// A message the caller supplied wins; generating one is what happens when
	// nobody wrote it. The commit tool writes its own, and the model that made
	// the change is better placed to say why than a side model reading the
	// diff afterwards.
	message = strings.TrimSpace(want)
	if message == "" && r.Message != nil {
		diffArgs := append([]string{"diff", "--cached", "--"}, fnames...)
		diffs, _ := r.git(diffArgs...)
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
	commitArgs = append(commitArgs, "--")
	commitArgs = append(commitArgs, fnames...)
	if _, err := r.git(commitArgs...); err != nil {
		return "", "", false, err
	}

	short, err := r.git("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", "", false, err
	}
	return strings.TrimSpace(short), message, true, nil
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
// Returns the final hashes of the commits in the range, newest first: the
// replacement for each rewritten one, the original for each that passed
// through. fromSHA empty or equal to the current HEAD means no new commits.
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
		if !isMerge && !foreign && trailerLine(msg, "Assisted-by") == "" {
			msg = strings.TrimRight(msg, "\n") + "\n\n" + trailer + "\n"
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
	newHead := rewrite[shas[len(shas)-1]]
	if err := r.moveHead(newHead); err != nil {
		return nil, err
	}
	final := make([]string, 0, len(shas))
	for i := range slices.Backward(shas) {
		final = append(final, rewrite[shas[i]])
	}
	return final, nil
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

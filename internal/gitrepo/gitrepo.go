// Package gitrepo implements the coder's git port by shelling out to the
// git binary — always argv, never a shell string.
// Author and committer identity are left alone: attribution is a single
// sanitized "Assisted-by: <model> via Strument" trailer on auto-commits.
package gitrepo

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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
	// context (the weak-model call); nil or an empty result falls
	// back to aider's "(no commit message provided)".
	Message func(diffs, context string) string
}

// Discover finds the repository containing dir, or returns an error when
// dir is not inside a git worktree (or git is not installed).
func Discover(dir string) (*Repo, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
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
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...) //nolint:gosec // Argv-only git invocation, never a shell string.
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
func (r *Repo) Commit(fnames []string, context, prepared string, attributed bool) (hash, message string, ok bool, err error) {
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

	// A prepared message means the caller already has one — the model wrote it
	// during the turn — so the generator is not consulted and no second request
	// goes out. That is the whole point of the commit_message tool.
	if prepared != "" {
		message = strings.TrimSpace(prepared)
	} else if r.Message != nil {
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

	commitArgs := []string{"commit", "-m", message}
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

// InCommit reports whether rel exists in the given commit's tree.
func (r *Repo) InCommit(commitish, rel string) bool {
	return r.ok("cat-file", "-e", commitish+":"+rel)
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

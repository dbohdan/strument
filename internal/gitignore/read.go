// This file is Strument's, not go-git's. It replaces upstream's dir.go, which
// reads ignore files through the go-billy filesystem abstraction and eagerly
// recurses the whole worktree. Strument reads through os and lets the caller
// pull in each directory's patterns as it walks, so a tree is traversed once
// rather than twice.

package gitignore

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	commentPrefix   = "#"
	gitignoreFile   = ".gitignore"
	infoExcludeFile = ".git/info/exclude"
)

// ReadIgnoreFile parses one ignore file into patterns scoped to domain, an
// ordered prefix of path components (nil at the worktree root). A file that
// does not exist yields no patterns and no error: an absent .gitignore is the
// normal case, not a failure.
//
// The line filter matches upstream's readIgnoreFile — skip comments and
// blank lines, hand everything else to ParsePattern, which owns the escaping
// and negation rules.
func ReadIgnoreFile(path string, domain []string) ([]Pattern, error) {
	f, err := os.Open(path)
	if err != nil {
		// ENOTDIR is absence too, and the case that matters is a git worktree:
		// there .git is a *file* holding "gitdir: …", so .git/info/exclude
		// resolves a path component that is not a directory. That is a
		// different errno from ENOENT, so it used to propagate — and since
		// ReadRoot feeds the file walk, every observation tool (ls, glob, grep,
		// symbol) failed outright inside a worktree rather than degrading.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var ps []Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		s := scanner.Text()
		if strings.TrimSpace(s) == "" || strings.HasPrefix(s, commentPrefix) {
			continue
		}
		ps = append(ps, ParsePattern(s, domain))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ps, nil
}

// ReadDir reads dir's own .gitignore, scoped to domain. Call it for each
// directory as a walk descends and append the result to the patterns already
// in scope: order is ascending priority, so a nested .gitignore overrides the
// one above it, exactly as git resolves them.
func ReadDir(dir string, domain []string) ([]Pattern, error) {
	return ReadIgnoreFile(filepath.Join(dir, gitignoreFile), domain)
}

// ReadRoot reads the worktree-root patterns in ascending priority:
// .git/info/exclude first, then the root .gitignore.
//
// Patterns from the user's core.excludesFile are deliberately not read. Upstream
// reaches them through go-git's gitconfig decoder, which is a much larger
// dependency than the matcher itself, and a per-user ignore file should not
// silently change which files Strument will show a model on someone else's
// machine.
func ReadRoot(root string) ([]Pattern, error) {
	ps, err := ReadIgnoreFile(filepath.Join(root, filepath.FromSlash(infoExcludeFile)), nil)
	if err != nil {
		return nil, err
	}
	rootPs, err := ReadDir(root, nil)
	if err != nil {
		return nil, err
	}
	return append(ps, rootPs...), nil
}

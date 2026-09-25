package coder

import (
	"path/filepath"
	"slices"

	"dbohdan.com/strument/internal/workspace"
)

// AgentsLocalFileName is the user's private counterpart to AGENTS.md: standing
// instructions for this checkout that are not shared. The name follows
// DeepSeek Harness (AGENTS.local.md) and Claude Code (CLAUDE.local.md), which
// load it after the shared file; Codex and Pi call theirs AGENTS.override.md
// and read it instead of AGENTS.md. The AGENTS.md spec itself defines neither.
//
// It exists so that private notes get the same review surface as shared ones.
// A memory tool that writes notes nobody sees — maki's, which a benchmark
// session used to leave notes no later session would read — is the
// alternative this replaces: here a note is an edit, with a diff, a snapshot,
// and an /undo.
const AgentsLocalFileName = "AGENTS.local.md"

// localExcluder is the optional part of a Repo that can keep a path out of
// git's view without touching a shared file. gitrepo implements it with
// .git/info/exclude; the port does not require it.
type localExcluder interface {
	ExcludeLocally(rel string) error
}

// agentsLocalRel is AGENTS.local.md's path relative to the repository root,
// or "" when there is no repository to keep it out of.
func (c *Coder) agentsLocalRel() string {
	if c.Repo == nil || c.Repo.Root() == "" {
		return ""
	}
	// Both sides resolved: git reports its root with symlinks followed, and
	// the coder's root is the path Strument was given. On macOS /var is
	// /private/var, and on Windows a temporary directory can arrive as an 8.3
	// short name, so the unresolved pair led out of the repository and the
	// file went unexcluded.
	rel, err := filepath.Rel(workspace.ResolveSymlinks(c.Repo.Root()),
		workspace.ResolveSymlinks(filepath.Join(c.Root, AgentsLocalFileName)))
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// KeepAgentsLocalUncommitted makes sure git does not offer AGENTS.local.md
// for commit, adding it to .git/info/exclude when nothing ignores it yet. It
// reports whether it changed anything, so the caller can say so once: this is
// a write to the user's repository, however small.
//
// A tracked AGENTS.local.md is left alone. Someone committed it on purpose,
// and an exclude entry would not untrack it anyway.
func (c *Coder) KeepAgentsLocalUncommitted() (excluded bool) {
	rel := c.agentsLocalRel()
	if rel == "" || c.Repo.GitIgnored(rel) || c.Repo.IsDirty(rel) || c.agentsLocalTracked(rel) {
		return false
	}
	ex, ok := c.Repo.(localExcluder)
	if !ok {
		return false
	}
	return ex.ExcludeLocally("/"+rel) == nil
}

func (c *Coder) agentsLocalTracked(rel string) bool {
	return slices.Contains(c.Repo.TrackedFiles(), rel)
}

// dropAgentsLocal takes an untracked AGENTS.local.md out of a list of paths
// to commit. It is private to this checkout, and staging an ignored path
// makes git refuse the whole add, so leaving it in would cost every other
// file in the commit too. An edited file that git calls dirty is tracked,
// and stays.
func (c *Coder) dropAgentsLocal(paths []string) []string {
	rel := c.agentsLocalRel()
	if rel == "" {
		return paths
	}
	out := paths[:0:0]
	for _, p := range paths {
		if filepath.ToSlash(p) == rel && !c.Repo.IsDirty(rel) {
			c.Out.Toolf("Not committing %s: it is private to this checkout.", AgentsLocalFileName)
			continue
		}
		out = append(out, p)
	}
	return out
}

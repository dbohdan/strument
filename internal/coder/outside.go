package coder

import (
	"os"
	"path/filepath"
	"strings"

	"dbohdan.com/strument/internal/workspace"
)

// Reads outside the project root, asked about rather than refused.
//
// They used to be refused outright, and the refusal pushed the model to the
// shell: asked to borrow from the user's own config, MiMo was told
// ~/.config/strument/config.star was outside the project and ran `cat` on it
// instead, which put the same read behind the shell gate's question with a
// worse tool. Asking keeps the decision where it was — with the user — and
// lets the model use the tool that numbers lines and leaves a record.
//
// The panel (October 2026) splits on this. OpenCode and Claude Code ask;
// Pi, Codex, Kimi Code and DeepSeek Harness read anywhere, the first two of
// those saying plainly that defending against the model is out of their
// scope. Strument treats content-borne injection as its problem, so it asks.
// From OpenCode comes the grant by directory: "a" covers everything under
// the directory for the rest of the run, so the third file in
// ~/.config/strument is not a third question.
//
// approve_model does not answer these. Its rubric asks about anything that
// "reaches outside project_root", which is every one of them, and it was
// measured on shell commands only.
//
// Secret-shaped files are refused before anyone is asked (workspace.outside).

// outsideGrants are the run's approvals, kept resolved: what is read is a
// symlink's target, so a grant has to be about targets too, or an approved
// directory holding a link to /etc would grant /etc.
type outsideGrants struct {
	paths map[string]bool
	dirs  map[string]bool
}

// outsideGranted is the workspace's OutsideGranted predicate.
func (c *Coder) outsideGranted(abs string) bool {
	if c.outside.paths == nil {
		return false
	}
	target := workspace.ResolveSymlinks(filepath.Clean(abs))
	if c.outside.paths[target] {
		return true
	}
	for dir := range c.outside.dirs {
		if target == dir || strings.HasPrefix(target, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// askOutside puts one outside read to the user. "" means granted; anything
// else is the model's answer.
func (c *Coder) askOutside(abs string, isDir bool) string {
	if c.outside.paths == nil {
		c.outside = outsideGrants{paths: map[string]bool{}, dirs: map[string]bool{}}
	}
	target := workspace.ResolveSymlinks(abs)
	shown := abs
	if target != abs {
		shown = abs + " → " + target
	}
	scope := target
	prompt := "List this directory outside the project?"
	what := "list that directory outside the project"
	if !isDir {
		scope = filepath.Dir(target)
		prompt = "Read this file outside the project?"
		what = "read that file outside the project"
	}
	req := ConfirmRequest{Prompt: prompt, Path: shown, Grant: GrantReadOutside}
	// No "all" for a directory that holds everything: an answer about the home
	// directory or the root is an answer about every file the user has, and
	// a one-key reply should not be able to give that.
	if !tooBroad(scope) {
		req.Group = "read-outside:" + scope
		req.GroupSession = true
		req.Scope = scope
	}
	res := c.confirmGrouped(req)
	if !res.Yes && !res.Always {
		c.Out.Toolf("Did not read %s (declined)", abs)
		return declined(res, what, GrantReadOutside)
	}
	if res.Always && req.Scope != "" {
		c.outside.dirs[scope] = true
		c.Out.Printf("Reading under %s without asking for the rest of this run.", scope)
		return ""
	}
	// A yes covers this path for the run: paging through a file is several
	// reads of it, and asking again at every window would teach "y" as reflex.
	c.outside.paths[target] = true
	return ""
}

// tooBroad reports a scope an "a" answer must not cover.
func tooBroad(dir string) bool {
	if dir == filepath.Dir(dir) { // a filesystem root
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		if dir == workspace.ResolveSymlinks(filepath.Clean(home)) || dir == filepath.Clean(home) {
			return true
		}
	}
	return false
}

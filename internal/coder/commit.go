package coder

import "strings"

// autoCommit commits edited files in git mode (§7.3); a no-op without a
// repo, with auto-commits off, or in dry-run. Returns the rotation message
// for moveBackCurMessages ("" => the no-git default path applies).
func (c *Coder) autoCommit(edited []string) string {
	if c.Repo == nil || !c.AutoCommits || c.DryRun {
		return ""
	}
	hash, message, ok, err := c.Repo.Commit(edited, c.commitContext())
	if err != nil {
		c.Out.Errorf("Unable to commit: %v", err)
		return ""
	}
	if !ok {
		return c.Prompts.FilesContentGPTNoEdits
	}
	c.lastCommitHash = hash
	c.Out.Printf("Commit %s %s", hash, message)
	return pyFormat(c.Prompts.FilesContentGPTEdits, map[string]string{
		"hash":    hash,
		"message": message,
	})
}

// commitContext summarizes the current messages for the commit-message
// model; refined with the git port in phase 8.
func (c *Coder) commitContext() string {
	var out string
	var outSb30 strings.Builder
	for _, m := range c.curMessages {
		outSb30.WriteString(m.Role + ": " + m.Text() + "\n")
	}
	out += outSb30.String()
	return out
}

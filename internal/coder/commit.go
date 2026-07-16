package coder

// autoCommit commits edited files in git mode (§7.3); a no-op without a
// repo, with auto-commits off, or in dry-run. Returns the rotation message
// for moveBackCurMessages ("" => the no-git default path applies).
func (c *Coder) autoCommit(edited []string) string {
	if c.Repo == nil || !c.AutoCommits || c.DryRun {
		return ""
	}
	hash, message, ok, err := c.Repo.Commit(edited, c.commitContext())
	if err != nil {
		c.Out.Error("Unable to commit: %v", err)
		return ""
	}
	if !ok {
		return c.Prompts.FilesContentGPTNoEdits
	}
	c.lastCommitHash = hash
	c.Out.Print("Commit %s %s", hash, message)
	return pyFormat(c.Prompts.FilesContentGPTEdits, map[string]string{
		"hash":    hash,
		"message": message,
	})
}

// commitContext summarizes the current messages for the commit-message
// model; refined with the git port in phase 8.
func (c *Coder) commitContext() string {
	var out string
	for _, m := range c.curMessages {
		out += m.Role + ": " + m.Text() + "\n"
	}
	return out
}

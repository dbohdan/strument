package coder

import (
	"context"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// autoCommit commits edited files in git mode; a no-op without a
// repo, with auto-commits off, or in dry-run. Returns the rotation message
// for moveBackCurMessages ("" => the no-git default path applies).
func (c *Coder) autoCommit(edited []string) string {
	if c.Repo == nil || !c.AutoCommits || c.DryRun {
		return ""
	}
	hash, message, ok, err := c.Repo.Commit(edited, c.commitContext(), true)
	if err != nil {
		// Commit failure after write leaves the edits in place.
		c.Out.Errorf("Unable to commit: %v", err)
		return ""
	}
	if !ok {
		return c.Prompts.FilesContentGPTNoEdits
	}
	c.lastCommitHash = hash
	if c.sessionCommits == nil {
		c.sessionCommits = map[string]bool{}
	}
	c.sessionCommits[hash] = true
	c.Out.Toolf("Commit %s %s", hash, message)
	return pyFormat(c.Prompts.FilesContentGPTEdits, map[string]string{
		"hash":    hash,
		"message": message,
	})
}

// commitContext formats curMessages for the commit-message model (aider's
// get_context_from_history).
func (c *Coder) commitContext() string {
	var b strings.Builder
	for _, m := range c.curMessages {
		b.WriteString("\n" + strings.ToUpper(m.Role) + ": " + m.Text() + "\n")
	}
	return b.String()
}

// commitMessageTimeout bounds the weak-model commit-message call; on
// timeout the commit proceeds with the fallback message.
const commitMessageTimeout = 60 * time.Second

// CommitMessenger returns a commit-message generator backed by a model —
// the weak-model call, packaged as the git port's Message func. An
// empty return means "no message" and the caller falls back.
func CommitMessenger(cl llm.ModelClient, model *config.Model, language string) func(diffs, context string) string {
	return func(diffs, chatContext string) string {
		languageInstruction := ""
		if language != "" {
			languageInstruction = "\n- Is written in " + language + "."
		}
		system := pyFormat(prompts.CommitSystem, map[string]string{
			"language_instruction": languageInstruction,
		})

		content := ""
		if chatContext != "" {
			content = chatContext + "\n"
		}
		content += "# Diffs:\n" + diffs

		ctx, cancel := context.WithTimeout(context.Background(), commitMessageTimeout)
		defer cancel()

		var answer strings.Builder
		for ev, err := range cl.Send(ctx, llm.Request{
			Model: model.Slug,
			Messages: []llm.Message{
				llm.TextMessage("system", system),
				llm.TextMessage("user", content),
			},
			ReasoningEffort: model.Reasoning,
			Temperature:     model.Temperature,
			ExtraParams:     model.RequestExtraParams(),
		}) {
			if err != nil {
				return ""
			}
			if ev.Kind == llm.EventAnswer {
				answer.WriteString(ev.Text)
			}
		}
		return strings.TrimSpace(answer.String())
	}
}

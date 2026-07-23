package coder

import (
	"errors"
	"os"
	"runtime"
	"strings"

	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
)

// chatChunks mirrors aider's ChatChunks: the canonical slot order is
// system + examples + readonly_files + repo + done + chat_files + cur +
// reminder.
type chatChunks struct {
	system        []llm.Message
	examples      []llm.Message
	done          []llm.Message
	repo          []llm.Message
	readonlyFiles []llm.Message
	chatFiles     []llm.Message
	cur           []llm.Message
	reminder      []llm.Message
}

func (ch *chatChunks) allMessages() []llm.Message {
	out := make([]llm.Message, 0,
		len(ch.system)+len(ch.examples)+len(ch.readonlyFiles)+len(ch.repo)+
			len(ch.done)+len(ch.chatFiles)+len(ch.cur)+len(ch.reminder))
	out = append(out, ch.system...)
	out = append(out, ch.examples...)
	out = append(out, ch.readonlyFiles...)
	out = append(out, ch.repo...)
	out = append(out, ch.done...)
	out = append(out, ch.chatFiles...)
	out = append(out, ch.cur...)
	out = append(out, ch.reminder...)
	return out
}

// addCacheControl decorates the last message of a slot with a cache
// breakpoint, on a clone (the source mutates in place; we keep history
// read-only through the alias).
func addCacheControl(messages []llm.Message) {
	if len(messages) == 0 {
		return
	}
	last := messages[len(messages)-1]
	block := llm.ContentBlock{
		Type:         "text",
		Text:         last.Content.String(),
		CacheControl: &llm.CacheControl{Type: "ephemeral"},
	}
	messages[len(messages)-1] = llm.Message{
		Role:    last.Role,
		Content: llm.Content{Blocks: []llm.ContentBlock{block}},
	}
}

// addCacheControlHeaders places at most 3 breakpoints: examples-else-system,
// repo-else-readonly, chat_files. Never on done/cur.
func (ch *chatChunks) addCacheControlHeaders() {
	if len(ch.examples) > 0 {
		addCacheControl(ch.examples)
	} else {
		addCacheControl(ch.system)
	}
	if len(ch.repo) > 0 {
		addCacheControl(ch.repo)
	} else {
		addCacheControl(ch.readonlyFiles)
	}
	addCacheControl(ch.chatFiles)
}

// allFences is the escalation list; shared with editblock.
var allFences = editblock.AllFences

// chooseFence scans chat + read-only content and picks the first fence
// whose open/close begins no line; unreadable chat files are dropped with a
// warning — an observable state mutation during assembly.
func (c *Coder) chooseFence() {
	var allContent strings.Builder
	for _, content := range c.absFnamesContent() {
		allContent.WriteString(content)
		allContent.WriteString("\n")
	}
	for _, fname := range c.absReadOnlyFnames {
		if data, err := os.ReadFile(fname); err == nil {
			allContent.Write(data)
			allContent.WriteString("\n")
		}
	}

	lines := strings.Split(allContent.String(), "\n")
	for _, f := range allFences {
		bad := false
		for _, line := range lines {
			if strings.HasPrefix(line, f.Open) || strings.HasPrefix(line, f.Close) {
				bad = true
				break
			}
		}
		if !bad {
			c.fence = fence{f.Open, f.Close}
			return
		}
	}
	c.fence = fence{allFences[0].Open, allFences[0].Close}
	c.Out.Warningf("Unable to find a fencing strategy! Falling back to: %s...%s", c.fence.open, c.fence.close)
}

// absFnamesContent reads chat files in order. A file that does not exist yet is
// kept with empty content (a to-be-created file); other read failures drop the
// file from the chat with a warning.
func (c *Coder) absFnamesContent() []string {
	var contents []string
	var kept []string
	for _, fname := range c.absFnames {
		data, err := os.ReadFile(fname)
		switch {
		case err == nil:
			kept = append(kept, fname)
			contents = append(contents, string(data))
		case errors.Is(err, os.ErrNotExist):
			// A file that does not exist yet is a to-be-created file: keep it in
			// the chat with empty content so the model can write it. It is
			// created on apply, never here.
			kept = append(kept, fname)
			contents = append(contents, "")
		default:
			c.Out.Warningf("Dropping %s from the chat.", c.relFname(fname))
		}
	}
	c.absFnames = kept
	return contents
}

// filesContent renders editable files: "\n{rel}\n{fence0}\n{content}{fence1}\n"
// per file, sorted for determinism (a divergence from aider, which uses set order).
func (c *Coder) filesContent() string {
	type entry struct{ rel, content string }
	contents := c.absFnamesContent()
	entries := make([]entry, 0, len(c.absFnames))
	for i, fname := range c.absFnames {
		entries = append(entries, entry{c.relFname(fname), contents[i]})
	}
	sortEntries := func(es []entry) {
		for i := 1; i < len(es); i++ {
			for j := i; j > 0 && es[j].rel < es[j-1].rel; j-- {
				es[j], es[j-1] = es[j-1], es[j]
			}
		}
	}
	sortEntries(entries)
	var b strings.Builder
	for _, e := range entries {
		b.WriteString("\n")
		b.WriteString(e.rel)
		b.WriteString("\n" + c.fence.open + "\n")
		b.WriteString(e.content)
		b.WriteString(c.fence.close + "\n")
	}
	return b.String()
}

func (c *Coder) readOnlyFilesContent() string {
	type entry struct{ rel, content string }
	var entries []entry
	for _, fname := range c.absReadOnlyFnames {
		data, err := os.ReadFile(fname)
		if err != nil {
			continue
		}
		entries = append(entries, entry{c.relFname(fname), string(data)})
	}
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].rel < entries[j-1].rel; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString("\n")
		b.WriteString(e.rel)
		b.WriteString("\n" + c.fence.open + "\n")
		b.WriteString(e.content)
		b.WriteString(c.fence.close + "\n")
	}
	return b.String()
}

// platformText renders the {platform} slot from injected PlatformInfo.
func (c *Coder) platformText() string {
	var b strings.Builder
	b.WriteString("- Platform: " + c.Platform.Platform + "\n")
	b.WriteString("- Shell: " + c.Platform.ShellVar + "=" + c.Platform.ShellVal + "\n")
	if c.Platform.Language != "" {
		b.WriteString("- Language: " + c.Platform.Language + "\n")
	}
	b.WriteString("- Current date: " + c.Platform.Date + "\n")
	if c.Platform.InGit {
		b.WriteString("- The user is operating inside a git repository\n")
	}
	return b.String()
}

// fmtSystemPrompt substitutes the template slots.
func (c *Coder) fmtSystemPrompt(prompt string) string {
	var finalReminders []string
	if c.Platform.Language != "" {
		finalReminders = append(finalReminders, "Reply in "+c.Platform.Language+".\n")
	}

	platformText := c.platformText()

	var shellCmdPrompt, shellCmdReminder, renameWithShell string
	if c.SuggestShellCommands {
		shellCmdPrompt = pyFormat(c.Prompts.ShellCmdPrompt, map[string]string{"platform": platformText})
		shellCmdReminder = pyFormat(c.Prompts.ShellCmdReminder, map[string]string{"platform": platformText})
		renameWithShell = c.Prompts.RenameWithShell
	} else {
		shellCmdPrompt = pyFormat(c.Prompts.NoShellCmdPrompt, map[string]string{"platform": platformText})
		shellCmdReminder = pyFormat(c.Prompts.NoShellCmdReminder, map[string]string{"platform": platformText})
	}

	language := c.Platform.Language
	if language == "" {
		language = "the same language they are using"
	}

	quadBacktickReminder := ""
	if c.fence.open == "````" {
		quadBacktickReminder = "\nIMPORTANT: Use *quadruple* backticks ```` as fences, not triple backticks!\n"
	}

	return pyFormat(prompt, map[string]string{
		"fence[0]":               c.fence.open,
		"fence[1]":               c.fence.close,
		"quad_backtick_reminder": quadBacktickReminder,
		"final_reminders":        strings.Join(finalReminders, "\n\n"),
		"platform":               platformText,
		"shell_cmd_prompt":       shellCmdPrompt,
		"rename_with_shell":      renameWithShell,
		"shell_cmd_reminder":     shellCmdReminder,
		"go_ahead_tip":           c.Prompts.GoAheadTip,
		"language":               language,
	})
}

// repoMapContent asks the repo map with the three-step fallback of
// get_repo_map.
func (c *Coder) repoMapContent() string {
	if c.RepoMap == nil || !c.Model.RepoMap {
		return ""
	}
	curText := c.curMessageText()
	mentionedFnames := c.fileMentions(curText, false)
	mentionedIdents := identMentions(curText)
	for f := range c.identFilenameMatches(mentionedIdents) {
		mentionedFnames[f] = true
	}

	allAbs := map[string]bool{}
	for _, rel := range c.allRelativeFiles() {
		allAbs[c.absRootPath(rel)] = true
	}
	chatSet := map[string]bool{}
	for _, f := range c.absFnames {
		chatSet[f] = true
	}
	for _, f := range c.absReadOnlyFnames {
		if allAbs[f] {
			chatSet[f] = true
		}
	}
	var chatFiles, otherFiles []string
	for f := range allAbs {
		if chatSet[f] {
			chatFiles = append(chatFiles, f)
		} else {
			otherFiles = append(otherFiles, f)
		}
	}

	content := c.RepoMap.GetRepoMap(chatFiles, otherFiles, mentionedFnames, mentionedIdents)
	if content == "" {
		var all []string
		for f := range allAbs {
			all = append(all, f)
		}
		content = c.RepoMap.GetRepoMap(nil, all, mentionedFnames, mentionedIdents)
	}
	if content == "" {
		var all []string
		for f := range allAbs {
			all = append(all, f)
		}
		content = c.RepoMap.GetRepoMap(nil, all, nil, nil)
	}
	return content
}

func (c *Coder) curMessageText() string {
	var b strings.Builder
	for _, m := range c.curMessages {
		b.WriteString(m.Text())
		b.WriteString("\n")
	}
	return b.String()
}

// formatChatChunks builds the canonical slots.
func (c *Coder) formatChatChunks() *chatChunks {
	c.chooseFence()

	mainSys := c.fmtSystemPrompt(c.Prompts.MainSystem)
	if c.SystemPromptPrefix != "" {
		mainSys = c.SystemPromptPrefix + "\n" + mainSys
	}

	var exampleMessages []llm.Message
	if c.ExamplesAsSysMsg {
		if len(c.Prompts.ExampleMessages) > 0 {
			mainSys += "\n# Example conversations:\n\n"
		}
		var mainSysSb317 strings.Builder
		for _, msg := range c.Prompts.ExampleMessages {
			mainSysSb317.WriteString("## " + strings.ToUpper(msg.Role) + ": " + c.fmtSystemPrompt(msg.Content) + "\n\n")
		}
		mainSys += mainSysSb317.String()
		mainSys = strings.TrimSpace(mainSys)
	} else {
		for _, msg := range c.Prompts.ExampleMessages {
			exampleMessages = append(exampleMessages, llm.TextMessage(msg.Role, c.fmtSystemPrompt(msg.Content)))
		}
		if len(c.Prompts.ExampleMessages) > 0 {
			exampleMessages = append(exampleMessages,
				llm.TextMessage("user", "I switched to a new code base. Please don't consider the above files or try to edit them any longer."),
				llm.TextMessage("assistant", "Ok."),
			)
		}
	}

	if c.Prompts.SystemReminder != "" {
		mainSys += "\n" + c.fmtSystemPrompt(c.Prompts.SystemReminder)
	}

	chunks := &chatChunks{}

	if c.UseSystemPrompt {
		chunks.system = []llm.Message{llm.TextMessage("system", mainSys)}
	} else {
		chunks.system = []llm.Message{
			llm.TextMessage("user", mainSys),
			llm.TextMessage("assistant", "Ok."),
		}
	}

	chunks.examples = exampleMessages
	chunks.done = c.doneMessages

	if repoContent := c.repoMapContent(); repoContent != "" {
		other := ""
		if len(c.absFnames) > 0 {
			other = "other "
		}
		_ = other // the {other} slot is substituted by repomap's prefix handling
		chunks.repo = []llm.Message{
			llm.TextMessage("user", repoContent),
			llm.TextMessage("assistant", "Ok, I won't try and edit those files without asking first."),
		}
	}

	if roContent := c.readOnlyFilesContent(); roContent != "" {
		chunks.readonlyFiles = []llm.Message{
			llm.TextMessage("user", c.Prompts.ReadOnlyFilesPrefix+roContent),
			llm.TextMessage("assistant", "Ok, I will use these files as references."),
		}
	}

	var filesContent, filesReply string
	switch {
	case len(c.absFnames) > 0:
		filesContent = c.Prompts.FilesContentPrefix + c.filesContent()
		filesReply = c.Prompts.FilesContentAssistantReply
	case len(chunks.repo) > 0 && c.Prompts.FilesNoFullFilesWithRepoMap != "":
		filesContent = c.Prompts.FilesNoFullFilesWithRepoMap
		filesReply = c.Prompts.FilesNoFullFilesWithRepoMapReply
	default:
		filesContent = c.Prompts.FilesNoFullFiles
		filesReply = "Ok."
	}
	if filesContent != "" {
		chunks.chatFiles = []llm.Message{
			llm.TextMessage("user", filesContent),
			llm.TextMessage("assistant", filesReply),
		}
	}

	chunks.cur = append([]llm.Message(nil), c.curMessages...)
	chunks.reminder = nil

	// Reminder gate: unknown max => always add; else add
	// iff base + candidate < max - margin, margin = min(1024, 5%).
	if c.Prompts.SystemReminder != "" {
		reminderText := c.fmtSystemPrompt(c.Prompts.SystemReminder)
		baseTokens := c.countMessages(chunks.allMessages())
		candTokens := c.Tokens.Count(reminderText)
		maxInput := c.Model.Context

		add := maxInput <= 0
		if !add {
			margin := min(1024, maxInput/20)
			add = baseTokens+candTokens < maxInput-margin
		}

		if add {
			switch c.ReminderPlacement {
			case "sys":
				chunks.reminder = []llm.Message{llm.TextMessage("system", reminderText)}
			case "user":
				if n := len(chunks.cur); n > 0 && chunks.cur[n-1].Role == "user" {
					chunks.cur[n-1] = llm.TextMessage("user", chunks.cur[n-1].Text()+"\n\n"+reminderText)
				}
			}
		}
	}

	return chunks
}

// formatMessages = formatChatChunks + cache decoration; decoration
// applies only when the provider supports caching.
func (c *Coder) formatMessages() *chatChunks {
	chunks := c.formatChatChunks()
	if c.cacheHeadersEnabled() {
		chunks.addCacheControlHeaders()
	}
	return chunks
}

// cacheHeadersEnabled: v1 keeps explicit cache-control decoration off by
// default (OpenAI-dialect endpoints cache implicitly; the placement logic
// stays tested for when a config toggle lands).
func (c *Coder) cacheHeadersEnabled() bool { return c.CacheHeaders }

func (c *Coder) countMessages(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += c.Tokens.Count(m.Text())
	}
	return n
}

func osName() string   { return strings.ToTitle(runtime.GOOS[:1]) + runtime.GOOS[1:] }
func archName() string { return runtime.GOARCH }

// detectUserLanguage: explicit config, else LANG-style env vars, normalized
// through a small fallback map (aider prefers Babel; we keep the fallback).
func detectUserLanguage(explicit string) string {
	if explicit != "" {
		return normalizeLanguage(explicit)
	}
	for _, envVar := range []string{"LANG", "LANGUAGE", "LC_ALL", "LC_MESSAGES"} {
		if v := os.Getenv(envVar); v != "" {
			return normalizeLanguage(strings.SplitN(v, ".", 2)[0])
		}
	}
	return ""
}

var languageNames = map[string]string{
	"en": "English", "fr": "French", "es": "Spanish", "de": "German",
	"it": "Italian", "pt": "Portuguese", "zh": "Chinese", "ja": "Japanese",
	"ko": "Korean", "ru": "Russian",
}

func normalizeLanguage(code string) string {
	if code == "" {
		return ""
	}
	up := strings.ToUpper(code)
	if up == "C" || up == "POSIX" {
		return ""
	}
	if len(code) > 3 && !strings.ContainsAny(code, "_-") && code[0] >= 'A' && code[0] <= 'Z' {
		return code
	}
	primary := strings.ToLower(strings.SplitN(strings.ReplaceAll(code, "-", "_"), "_", 2)[0])
	if name, ok := languageNames[primary]; ok {
		return name
	}
	return code
}

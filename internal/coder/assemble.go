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
// system + examples + readonly_files + done + chat_files + cur.
//
// aider has a repo slot between readonly_files and done, carrying the ranked
// repository map. Strument no longer fills it. The map answered "how does the
// model find code when it cannot look", and the model can look now — it greps —
// so the map was a per-turn tax on every send for a digest it did not read.
// The ranked rendering has since gone entirely: /map became /symbol, because
// "where is this defined" is the question a reader actually has.
type chatChunks struct {
	system        []llm.Message
	examples      []llm.Message
	done          []llm.Message
	readonlyFiles []llm.Message
	chatFiles     []llm.Message
	cur           []llm.Message
}

func (ch *chatChunks) allMessages() []llm.Message {
	out := make([]llm.Message, 0,
		len(ch.system)+len(ch.examples)+len(ch.readonlyFiles)+
			len(ch.done)+len(ch.chatFiles)+len(ch.cur))
	out = append(out, ch.system...)
	out = append(out, ch.examples...)
	out = append(out, ch.readonlyFiles...)
	out = append(out, ch.done...)
	out = append(out, ch.chatFiles...)
	out = append(out, ch.cur...)
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
		Type: "text",
		Text: last.Content.String(),
		// The 1-hour TTL matches Strument's cadence: bursts of turns separated
		// by think-time gaps that the default ~5-minute cache would let expire.
		CacheControl: &llm.CacheControl{Type: "ephemeral", TTL: "1h"},
	}
	messages[len(messages)-1] = llm.Message{
		Role:    last.Role,
		Content: llm.Content{Blocks: []llm.ContentBlock{block}},
	}
}

// addCacheControlHeaders places at most 3 breakpoints: examples-else-system,
// read-only files, chat_files. Never on done/cur.
//
// The repo map used to hold the second one, and was the reason the map had to
// be frozen: a prefix that changes every turn caches nothing. With the map out
// of the prompt the prefix is stable by construction.
func (ch *chatChunks) addCacheControlHeaders() {
	if len(ch.examples) > 0 {
		addCacheControl(ch.examples)
	} else {
		addCacheControl(ch.system)
	}
	addCacheControl(ch.readonlyFiles)
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
//
// Three slots remain. The fence and shell-command slots went with the text edit
// formats: fences framed SEARCH/REPLACE blocks, and the shell-command guidance
// described a format where commands were prose the harness parsed back out.
// Both are the schema's job now.
func (c *Coder) fmtSystemPrompt(prompt string) string {
	var finalReminders []string
	if c.Platform.Language != "" {
		finalReminders = append(finalReminders, "Reply in "+c.Platform.Language+".\n")
	}

	language := c.Platform.Language
	if language == "" {
		language = "the same language they are using"
	}

	return pyFormat(prompt, map[string]string{
		"final_reminders": strings.Join(finalReminders, "\n\n"),
		"platform":        c.platformText(),
		"language":        language,
	})
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

	// The reminder rides in the system prompt and nowhere else.
	//
	// aider also appends it again at the end of the conversation — rules at the
	// top and at the bottom, the standard hedge against instructions getting
	// lost in the middle. Strument carried that, so the editing rules went out
	// twice in every request. Claude Haiku reported it when asked what looked
	// odd about the harness, and a probe confirmed it: two copies per send,
	// stable across turns.
	//
	// The end copy is the one that went. The system prompt is a stable prefix
	// and therefore cacheable, where a copy appended to the last user turn is
	// regenerated every send. The tool schema now carries most of the format
	// rules and travels beside the messages, so recency buys less than it did
	// when a SEARCH/REPLACE block had to be parsed out of prose. And the "user"
	// placement worked by editing words into the user's own message, which is
	// one fewer place the harness pretends the user said something.
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

// cacheHeadersEnabled reports whether the active model opts into prompt
// caching (the per-model `cache` config setting). It reads the model live so
// the gate follows a /model switch. When on, formatMessages decorates the slots
// with cache-control breakpoints. Explicit breakpoints matter on
// Anthropic-family models; on implicit-caching providers they are inert but a
// stable prefix still helps, so the flag is worth setting for any caching
// model. Nothing has to be frozen to keep that prefix stable any more — the
// repo map was the one part that changed every turn, and it is no longer in
// the prompt.
func (c *Coder) cacheHeadersEnabled() bool { return c.Model != nil && c.Model.Cache }

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

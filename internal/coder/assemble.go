package coder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// chatChunks mirrors aider's ChatChunks: the canonical slot order is
// system + examples + readonly_files + done + cur.
//
// aider's chat_files slot is gone. It carried the /add files' contents as a
// fabricated user turn with a fabricated assistant reply; the names now ride in
// the system prompt and the model reads the files itself.
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
	notes         []llm.Message
	done          []llm.Message
	readonlyFiles []llm.Message
	cur           []llm.Message
}

func (ch *chatChunks) allMessages() []llm.Message {
	out := make([]llm.Message, 0,
		len(ch.system)+len(ch.examples)+len(ch.notes)+len(ch.readonlyFiles)+
			len(ch.done)+len(ch.cur))
	out = append(out, ch.system...)
	out = append(out, ch.examples...)
	// Notes sit *before* the read-only files, deliberately. Breakpoints go on
	// examples-or-system and on read-only files, so anything between them rides
	// inside the cached prefix — and a mid-session /read-only, which rewrites
	// that block, does not invalidate the notes with it.
	out = append(out, ch.notes...)
	out = append(out, ch.readonlyFiles...)
	out = append(out, ch.done...)
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

// addCacheControlHeaders places at most 2 breakpoints: examples-else-system
// and read-only files. Never on done/cur.
//
// There used to be a third, on the chat-files block. That block changed every
// time the model edited a file, so it invalidated its own breakpoint on most
// editing turns. Pinned files are named in the system prompt now and their
// contents arrive as tool results, so the prefix moves only when the pin list
// does — on /add and /drop, not on every edit.
func (ch *chatChunks) addCacheControlHeaders() {
	if len(ch.examples) > 0 {
		addCacheControl(ch.examples)
	} else {
		addCacheControl(ch.system)
	}
	addCacheControl(ch.readonlyFiles)
}

// allFences is the escalation list; shared with editblock.
var allFences = editblock.AllFences

// chooseFence picks the first fence whose open/close begins no line in the
// read-only content, which is the only fenced content left. It still walks the
// chat files, because absFnamesContent is what drops an unreadable one from the
// chat with a warning — an observable state mutation during assembly.
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
			c.Out.Warningf("Dropping %s from the chat.", c.displayName(fname))
		}
	}
	c.absFnames = kept
	return contents
}

func (c *Coder) readOnlyFilesContent() string {
	type entry struct{ rel, content string }
	var entries []entry
	for _, fname := range c.absReadOnlyFnames {
		data, err := os.ReadFile(fname)
		if err != nil {
			continue
		}
		entries = append(entries, entry{c.displayName(fname), string(data)})
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
	if c.Platform.WorkDir != "" {
		b.WriteString("- Working directory: " + c.Platform.WorkDir + "\n")
	}
	if c.Platform.InGit {
		b.WriteString("- The user is operating inside a git repository\n")
		b.WriteString("- Git is available via the bash tool: git log, git diff, git status, etc.\n")
	}
	return b.String()
}

// codeToolsText renders the {code_tools} slot: the code bullet when the code
// tool is in this session's tool set, "" when it is not. The condition is the
// same one toolDefs uses, so the prose tracks the schema by construction
// rather than by a second copy of the condition drifting away from it.
//
// ObservationViaRunCode replaces the bullet with the force arm's paragraph: the
// bullet's "reach for it when one answer needs several lookups" presupposes a
// direct-call alternative that no longer exists, and would read as optional.
// The replacement states the arrangement as fact, in the same register —
// calm, specific — and carries the two facts the mode's description leans on:
// that results come back to the program, and that the last value is what
// returns to the model.
func (c *Coder) codeToolsText() string {
	if c.ObservationViaRunCode {
		return prompts.ObservationViaRunCodeParagraph
	}
	if !c.OfferCode {
		return ""
	}
	return prompts.CodeToolsBullet
}

// fmtSystemPrompt substitutes the template slots.
//
// Four slots remain. The fence and shell-command slots went with the text edit
// formats: fences framed SEARCH/REPLACE blocks, and the shell-command guidance
// described a format where commands were prose the harness parsed back out.
// Both are the schema's job now.
//
// {code_tools} is filled from the same condition that offers the run_code tool in
// the schema, so the prose and the schema cannot drift apart in either
// direction: a prompt naming a tool the model does not have, or a schema tool
// the prompt's cost list omits — the closed-world reading that produced the
// 0/36 uptake in doc/experiments/2026-08-code-mode.md.
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
		"final_reminders":   strings.Join(finalReminders, "\n\n"),
		"platform":          c.platformText(),
		"language":          language,
		"code_tools":        c.codeToolsText(),
		"observation_tools": c.observationToolsText(),
	})
}

// observationToolsText renders the {observation_tools} slot: the standard
// read/grep/glob/ls paragraph normally, "" under the force arm — where the
// ObservationViaRunCodeParagraph in {code_tools} has already said how observation
// works and naming the tools as directly callable would contradict the schema.
// The slot's condition is the same one toolDefs uses.
func (c *Coder) observationToolsText() string {
	if c.ObservationViaRunCode {
		return ""
	}
	if c.editFormat == "ask" {
		return prompts.AskObservationBullet
	}
	return prompts.ObservationBullet
}

// filesNoFullFilesText picks the nothing-pinned note: the standard one, or the
// force arm's variant, which must not tell the model to use tools the schema
// withholds. The swap follows the same flag the tool set does.
func (c *Coder) filesNoFullFilesText() string {
	if c.ObservationViaRunCode {
		if c.editFormat == "ask" {
			return prompts.AskFilesNoFullFilesViaCode
		}
		return prompts.FilesNoFullFilesViaCode
	}
	return c.Prompts.FilesNoFullFiles
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
		var examples strings.Builder
		for _, msg := range c.Prompts.ExampleMessages {
			examples.WriteString("## " + strings.ToUpper(msg.Role) + ": " + c.fmtSystemPrompt(msg.Content) + "\n\n")
		}
		mainSys += examples.String()
		mainSys = strings.TrimSpace(mainSys)
	} else {
		for _, msg := range c.Prompts.ExampleMessages {
			exampleMessages = append(exampleMessages, llm.TextMessage(msg.Role, c.fmtSystemPrompt(msg.Content)))
		}
		if len(c.Prompts.ExampleMessages) > 0 {
			// The bridge from illustration to the real conversation. It used to
			// be a fabricated user turn answered by a fabricated "Ok.", which
			// is two lies to draw one line; the harness can just say it.
			exampleMessages = append(exampleMessages, llm.HarnessNote(
				"The exchange above is an example, not part of this conversation. "+
					"The files and history that follow are the real ones."))
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

	// What /add pinned, in the harness's own voice. This used to be the files'
	// contents in a fabricated user turn answered by a fabricated assistant one;
	// it is now their names in the system prompt. See pinnedFilesNote.
	if note := c.pinnedFilesNote(); note != "" {
		mainSys += "\n\n" + note
	}

	chunks := &chatChunks{}

	// The system prompt goes in a system message, full stop.
	//
	// There used to be a UseSystemPrompt == false branch that sent it as a user
	// turn answered by a fabricated "Ok." — aider's shape for models with no
	// system role. It was unreachable: the field was set true in New and never
	// set false anywhere, by any flag or config. It is also no longer needed by
	// anything. Gemma was the last major family without a system role, and
	// Gemma 4 added native support for one in April 2026.
	chunks.system = []llm.Message{llm.TextMessage(llm.RoleSystem, mainSys)}

	chunks.examples = exampleMessages
	chunks.done = c.doneMessages

	// Read-only files keep their injection, and this is not an oversight. glob,
	// ls, and grep are scoped to the workspace root, so /read-only is the only
	// channel for a file *outside* the project — an out-of-tree spec, a sibling
	// repo's header. Instructing the model to go and find one, as /add now does,
	// would send it after something three of the four observation tools cannot
	// see.
	//
	// What did go is the fabricated assistant reply agreeing to it. Twelve live
	// sessions across three models with the honest prefix and no reply are in
	// doc/experiments/2026-08-readonly-honest.md: the contents were used as
	// readily as before, and the one case that had been going wrong — a request
	// to edit the reference — stopped producing stalled turns.
	if roContent := c.readOnlyFilesContent(); roContent != "" {
		chunks.readonlyFiles = []llm.Message{
			llm.TextMessage("user", c.Prompts.ReadOnlyFilesPrefix+roContent),
		}
	}

	// A system message, like the compaction summary and the context-exhausted
	// note. The notes are the harness's artifact: not something the user said,
	// and not something this model said either — a different model usually wrote
	// them, and a different model again is reading them now. The read-only block
	// above is a user message for historical reasons its own comment records;
	// that is the precedent not to follow.
	if notes := strings.TrimSpace(c.SessionNotes); notes != "" {
		chunks.notes = []llm.Message{llm.TextMessage(llm.RoleSystem,
			prompts.SessionNotesPrefix(c.SessionNotesDate)+"\n"+notes+"\n")}
	}

	chunks.cur = append([]llm.Message(nil), c.curMessages...)
	return chunks
}

// pinnedFilesNote is what the system prompt says about the files /add pinned.
//
// It replaces the file *contents* that used to ride in a fabricated user turn
// with the file *names* and an instruction to read them. Measured over 600
// samples against three models (doc/experiments/2026-08-add-instruct.md): the
// same task success, one extra step, and blind edits — a pinned file written
// without ever reading it — from 383 across 230 runs down to zero across none.
//
// It also removes Strument's last always-on synthetic turn. Everything the
// model learns about the project now arrives through a tool call, with no
// exception carved out for /add, and the harness stops asserting in the user's
// voice that a block of text is a file's current contents.
//
// A pinned file that does not exist yet is named separately. Telling the model
// to read it would send it after something that is not there; it is a file to
// create, and saying so is the whole of what it needs.
func (c *Coder) pinnedFilesNote() string {
	if len(c.absFnames) == 0 {
		return c.filesNoFullFilesText()
	}

	var existing, missing []string
	for _, fname := range c.absFnames {
		rel := c.displayName(fname)
		if _, err := os.Stat(fname); err == nil {
			existing = append(existing, rel)
		} else {
			missing = append(missing, rel)
		}
	}
	slices.Sort(existing)
	slices.Sort(missing)

	ask := c.editFormat == "ask"

	var b strings.Builder
	if len(existing) > 0 {
		fmt.Fprintf(&b, "The user has pinned %s to this session: %s.\n",
			plural(len(existing), "file", "files"), strings.Join(existing, ", "))
		// What pinning is, and what it is not.
		//
		// This used to say "These are the files they want changed", which
		// asserts an intention the act of pinning does not carry. A user pins
		// files to put them in front of the model; what to do with them is the
		// message's job. GLM-5.3 was caught reconciling the two out loud —
		// "the pinned files are for changes... but the user question is
		// analysis/proposal" — and had to talk itself back to answering the
		// question actually asked.
		b.WriteString("Pinning says what the conversation is about. It is not itself a " +
			"request to change anything: the user's message says what they want.\n")
		b.WriteString("Pinning puts their names here, not their contents: read a file before " +
			"you work on it, unless it is already in this conversation.\n")
	}
	if len(missing) > 0 {
		does, them := "do", "them"
		if len(missing) == 1 {
			does, them = "does", "it"
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "The user has also pinned %s that %s not exist yet: %s.\n",
			plural(len(missing), "file", "files"), does, strings.Join(missing, ", "))
		if ask {
			// No write tool here, so telling it to create anything is an
			// instruction it cannot follow.
			fmt.Fprintf(&b, "There is nothing to read in %s.\n", them)
		} else {
			fmt.Fprintf(&b, "Create %s with write; there is nothing to read.\n", them)
		}
	}
	// Naming AGENTS.md is what makes it work, and that is measured rather than
	// assumed. Over 24 live sessions on a rule contrary to habit — every
	// exported function gets a "Contract:" doc comment — compliance was 0/8
	// with no AGENTS.md at all, 2/8 with it merely pinned, and 6/8 with this
	// clause (none vs slot p=0.007). Pinned and unexplained, a model reads it as
	// one more source file it happens to have been given.
	//
	// It goes here rather than in MainSystem because it must not claim standing
	// instructions exist when none do: a project without the file should say
	// nothing about it.
	if slices.Contains(existing, AgentsFileName) {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s holds the project's standing instructions. "+
			"Follow them for every change you make here.\n", AgentsFileName)
	}
	return strings.TrimRight(b.String(), "\n")
}

// AgentsFileName is the cross-tool conventional name for a project's standing
// instructions (https://agents.md/). cmd/strument pins it; this is where the
// model is told what it is.
const AgentsFileName = "AGENTS.md"

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

// countTools estimates what the tool schemas cost on the wire.
//
// They are the last part of a request that /tokens could not see, and they are
// not small: nine tools with their descriptions and JSON Schema parameters are
// worth more than the system prompt on a fresh session. They also ride on
// *every* request, so a twelve-step turn pays for them twelve times.
//
// The envelope is mirrored from client.BuildBody rather than marshalling the
// ToolDefs directly, because the wire form wraps each one in
// {"type":"function","function":{…}} and counting the Go struct would
// under-report by that much on every tool. Duplicating four keys is the cheaper
// error: the alternative is for the coder to hold a client just to ask what a
// request weighs.
func (c *Coder) countTools() int {
	defs := c.toolDefs()
	if len(defs) == 0 {
		return 0
	}
	wire := make([]map[string]any, len(defs))
	for i, t := range defs {
		wire[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Parameters,
			},
		}
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return 0
	}
	return c.Tokens.Count(string(data))
}

// countMessages estimates the tokens a slice of messages will cost.
//
// Tool calls are counted, and that is not a refinement. m.Text() reads Content
// only, so an assistant message carrying nothing but tool calls counted as
// zero — and in this harness every action is a tool call, with an edit's
// old_string/new_string often the largest thing in the request. A twelve-step
// turn could report a few hundred tokens of history while sending several
// thousand. checkTokens counts the same way, so the guard that warns before a
// request overruns the declared window was blind to the same bytes.
//
// Still missing, deliberately for now: the tool *schemas*, which ride on every
// request and are worth on the order of a thousand tokens. They are a property
// of the request rather than of a message slice, so they belong in a row of
// their own rather than smuggled into this sum.
func (c *Coder) countMessages(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += c.Tokens.Count(m.Text())
		for _, tc := range m.ToolCalls {
			n += c.Tokens.Count(tc.Name) + c.Tokens.Count(tc.Arguments)
		}
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

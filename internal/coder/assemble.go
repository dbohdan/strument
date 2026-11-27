package coder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// chatChunks is a request's messages in their slots: the system prompt, the
// settled conversation, and the turn in progress.
//
// Nothing else rides in the prefix. File contents arrive as tool results when
// the model reads them, and there is no repository map: the model can look —
// it greps — so a map would be a per-turn tax on every send for a digest it
// does not read.
type chatChunks struct {
	system []llm.Message
	done   []llm.Message
	cur    []llm.Message
}

func (ch *chatChunks) allMessages() []llm.Message {
	out := make([]llm.Message, 0,
		len(ch.system)+
			len(ch.done)+len(ch.cur))
	out = append(out, ch.system...)
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
	messages[len(messages)-1] = withBreakpoint(messages[len(messages)-1])
}

// breakpoint is the cache marker every slot uses. The 1-hour TTL matches
// Strument's cadence: bursts of turns separated by think-time gaps that the
// default ~5-minute cache would let expire.
func breakpoint() *llm.CacheControl {
	return &llm.CacheControl{Type: "ephemeral", TTL: "1h"}
}

// withBreakpoint returns m with a cache breakpoint on its last content block.
// Plain text becomes one text block; a block list keeps its blocks — a tool
// result's image stays an image — and is copied before the marker goes on,
// because the blocks may be shared with the conversation history. A message
// with nothing to mark, such as an assistant turn that is only tool calls,
// comes back unchanged.
func withBreakpoint(m llm.Message) llm.Message {
	if m.Content.Text != nil {
		if *m.Content.Text == "" {
			return m
		}
		block := llm.TextBlock(*m.Content.Text)
		block.CacheControl = breakpoint()
		m.Content = llm.BlocksContent(block)
		return m
	}
	blocks := slices.Clone(m.Content.Blocks)
	for i, b := range slices.Backward(blocks) {
		if b.Type == llm.BlockText && b.Text == "" {
			continue // Anthropic rejects an empty text block, marked or not
		}
		blocks[i].CacheControl = breakpoint()
		m.Content = llm.Content{Blocks: blocks}
		return m
	}
	return m
}

// addCacheControlHeaders places breakpoints within Anthropic's limit of four:
// the system prompt, and two that roll with the conversation.
//
// The conversation needs its own. In a tool loop every step resends the whole
// conversation, and a provider that caches only up to an explicit breakpoint —
// Anthropic — bills all of it at full price on every step without one.
// Providers that cache prefixes on their own hide that. Claude Haiku 5.5 did
// not, when the conversation had none: a 20-step Larkspur turn restored with 480 messages sent 4.1 million
// tokens and read 135k of them from the cache, the ~7.5k-token system prompt
// once per step. With these two breakpoints, every token of a five-step turn
// was either a cache read or a cache write.
func (ch *chatChunks) addCacheControlHeaders(lastOnly bool) {
	addCacheControl(ch.system)
	ch.addConversationBreakpoints(lastOnly)
}

// addConversationBreakpoints marks the last message of the request, which
// writes the conversation so far to the cache, and the message before the
// last assistant turn, which is where the previous request put its last
// breakpoint. The second makes the read exact: Anthropic looks for an earlier
// cache entry only about twenty blocks back from a breakpoint, and one step
// of parallel tool calls can add more than that. Across turns it does the same
// job, since the previous turn's last request ended just before its answer.
//
// lastOnly is for a cache that uses only the request's last breakpoint
// (lastBreakpointOnly). There the one breakpoint goes on the last user
// message: within a turn that is the turn's own request, which does not move,
// so everything before it is cached once and read by every step, and the
// provider's implicit cache covers the steps themselves.
func (ch *chatChunks) addConversationBreakpoints(lastOnly bool) {
	n := len(ch.done) + len(ch.cur)
	if n == 0 {
		return
	}
	// done aliases the conversation history; cur is already a copy.
	ch.done = slices.Clone(ch.done)
	at := func(i int) *llm.Message {
		if i < len(ch.done) {
			return &ch.done[i]
		}
		return &ch.cur[i-len(ch.done)]
	}
	// Every user and tool message goes out as blocks, marked or not, so a
	// message keeps one shape from the step it is marked to the step it is
	// not. Plain text one step and a marked block the next changed the bytes
	// of the prefix under a provider that caches it on its own.
	for i := range n {
		if m := at(i); m.Role != llm.RoleAssistant && m.Content.Text != nil && *m.Content.Text != "" {
			m.Content = llm.BlocksContent(llm.TextBlock(*m.Content.Text))
		}
	}
	if lastOnly {
		for i := n - 1; i >= 0; i-- {
			if at(i).Role == llm.RoleUser {
				*at(i) = withBreakpoint(*at(i))
				return
			}
		}
		return
	}
	*at(n - 1) = withBreakpoint(*at(n - 1))
	for i := n - 1; i >= 1; i-- {
		if at(i).Role == llm.RoleAssistant {
			*at(i - 1) = withBreakpoint(*at(i - 1))
			return
		}
	}
}

// factsText is the part of the system prompt the harness writes about this
// session: the platform, the model, and the project's standing instructions.
// It follows the instructions — the built-in System or the config's
// system_prompt — and a replacement does not replace it, because it is not
// instructions but what is true here.
//
// It was the {platform} slot of a template once, spliced into the middle of
// the prompt. Facts at the end leave the instructions one literal string, and
// keep them a byte-stable prefix across projects and models.
func (c *Coder) factsText() string {
	var b strings.Builder
	b.WriteString("Keep in mind these details about the user's platform and environment:\n")
	// The distribution or product and its version, not the build target: it
	// decides apt or dnf, which sed -i, and which binaries to fetch, and WSL
	// changes paths and line endings while reporting itself as Linux.
	b.WriteString("- Operating system: " + c.Platform.Platform + "\n")
	// What the bash tool runs, not $SHELL. The line used to give the user's
	// login shell, which is never what a command runs in: on a fish user's
	// machine it read "SHELL=/usr/bin/fish", and Qwen3.8 27B answered it with
	// fish syntax and with commands wrapped in `bash -c`. Only with the tool,
	// like the git line below.
	if c.offersShell() {
		b.WriteString("- Shell: commands given to the bash tool run in Strument's built-in shell, " +
			"which takes bash syntax on every platform. It is not the user's login shell.\n")
	}
	// Not "Current date": the date is taken once, when Strument starts, so
	// after midnight that label would be the false part. The
	// date stays because a model without one assumes the year its training
	// ended, which is the costlier mistake; the about tool has the live clock.
	b.WriteString("- Date Strument started: " + c.Platform.Date +
		" (the about tool gives the current date and time)\n")
	if c.Platform.WorkDir != "" {
		b.WriteString("- Project root: " + c.Platform.WorkDir)
		if c.offersShell() {
			b.WriteString(" (each bash command starts here)")
		}
		b.WriteString("\n")
	}
	if l := c.Platform.LaunchDir; l != "" && l != c.Platform.WorkDir {
		// Relative when inside the project, which is the usual case and the
		// one that says where in it the user is.
		if rel, err := filepath.Rel(c.Platform.WorkDir, l); err == nil && filepath.IsLocal(rel) {
			b.WriteString("- The user started Strument in " + filepath.ToSlash(rel) + ", inside the project\n")
		} else {
			b.WriteString("- The user started Strument in " + l + "\n")
		}
	}
	if c.Platform.InGit {
		// The tools to use rather than the git commands every model knows,
		// and only the ones this session has: a model told about a tool it
		// does not have may call it (doc/experiments/2026-09-tool-disclosure,
		// arm C). The commit tool is always offered, but declines when
		// commits are off, so it is named only when they are on.
		line := "- The project is a git repository."
		switch {
		case c.offersShell() && c.Commits:
			line += " Read its history and changes with git through the bash tool, and commit with the commit tool rather than git commit."
		case c.offersShell():
			line += " Read its history and changes with git through the bash tool. Commits are off for this session."
		case c.Commits:
			line += " Commit with the commit tool."
		}
		b.WriteString(line + "\n")
	}
	// The model's own name, as the provider calls it. A model asked what it
	// is otherwise answers from training, which names an older model or the
	// wrong vendor, and one switched in by /model reads an earlier model's
	// turns as its own (the restore seam says so; this says who it is now).
	if c.Model != nil && c.Model.Slug != "" {
		b.WriteString("- Model: " + c.Model.Slug + "\n")
	}
	if note := c.agentsNote(); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// systemPrompt is the instructions this session sends: the config's
// system_prompt when it sets one, the built-in otherwise.
func (c *Coder) systemPrompt() string {
	if c.SystemPrompt != "" {
		return c.SystemPrompt
	}
	return prompts.System
}

// formatChatChunks builds the canonical slots.
func (c *Coder) formatChatChunks() *chatChunks {
	mainSys := strings.TrimRight(c.systemPrompt(), "\n") + "\n\n" + c.factsText()

	chunks := &chatChunks{}

	// The system prompt goes in a system message, full stop. No current model
	// family lacks a system role; Gemma, the last, gained one in April 2026.
	chunks.system = []llm.Message{llm.TextMessage(llm.RoleSystem, mainSys)}

	// The seams span both: the turn in progress may be the first of the
	// current model's after another's.
	all := markModelSeams(append(append([]llm.Message(nil), c.doneMessages...), c.curMessages...), c.Model.Slug)
	chunks.done = all[:len(c.doneMessages):len(c.doneMessages)]
	chunks.cur = all[len(c.doneMessages):]
	return chunks
}

// AgentsFileName is the cross-tool conventional name for a project's standing
// instructions (https://agents.md/). agentsNote is where the model is told
// what it is.
const AgentsFileName = "AGENTS.md"

// agentsNote names AGENTS.md and AGENTS.local.md when they exist, and asks the
// model to read them. Naming the file as standing instructions is what makes
// it work, and that is measured rather than assumed: over 24 live sessions on
// a rule contrary to habit, compliance was 0/8 with no AGENTS.md, 2/8 with it
// merely pinned, and 6/8 with this clause (doc/experiments/2026-08-agents-md).
//
// The model is told to read the files rather than handed their contents. That
// keeps one channel into the conversation for project files, the read tool,
// with what came back visible in the transcript, and it keeps a file the user
// did not write for the model out of a prompt the model takes as the harness.
// A project without the file says nothing about it, rather than claiming
// standing instructions exist when none do.
func (c *Coder) agentsNote() string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(c.Root, name))
		return err == nil
	}
	shared, local := has(AgentsFileName), has(AgentsLocalFileName)
	var b strings.Builder
	if shared {
		fmt.Fprintf(&b, "%s holds the project's standing instructions. Read it before "+
			"you start, and follow it for every change you make here.", AgentsFileName)
	}
	// The private file is told apart from the shared one so that a note lands
	// in the right place: a machine's paths or the user's own preferences in
	// the file nobody else reads, conventions for everyone in the one that is
	// committed.
	if local {
		if shared {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s holds the user's private instructions for this checkout. It is "+
			"not committed or shared. Read it too, and where it conflicts with %s, it wins.",
			AgentsLocalFileName, AgentsFileName)
	}
	return b.String()
}

// formatMessages = formatChatChunks + cache decoration; decoration
// applies only when the provider supports caching.
func (c *Coder) formatMessages() *chatChunks {
	chunks := c.formatChatChunks()
	if c.cacheHeadersEnabled() {
		chunks.addCacheControlHeaders(lastBreakpointOnly(c.Model.Slug))
	}
	return chunks
}

// lastBreakpointOnly reports whether the model's cache uses only the last
// breakpoint in a request, which OpenRouter documents for Gemini. There the
// rolling breakpoints backfire: the last one lands on a tool result, which
// creates no cache, and the system prompt's cache is not read either. A
// Gemini Flash run logged per request read 0 tokens from the cache on its
// second and third requests, where the system-only breakpoint had read 4,658
// on every one.
func lastBreakpointOnly(slug string) bool {
	return strings.Contains(strings.ToLower(slug), "gemini")
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
		n += c.Tokens.Count(m.Text()) + c.replayTokens(m)
		for _, tc := range m.ToolCalls {
			n += c.Tokens.Count(tc.Name) + c.Tokens.Count(tc.Arguments)
		}
	}
	return n
}

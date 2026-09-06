package coder

import (
	"context"
	"os"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// ConsultScope says how much of the session an advisor model is shown.
//
// The ladder exists because the right answer is not obvious and is measurable:
// an advisor that has read nothing gives generic advice, and an advisor that has
// read the whole transcript inherits the framing the user was trying to escape.
// doc/experiments/2026-09-consult.md is the trial that settles the default;
// until it runs, ConsultFiles is a provisional choice, not a finding.
type ConsultScope int

const (
	// ConsultNothing sends the question alone. This is what /btw does.
	ConsultNothing ConsultScope = iota
	// ConsultFiles adds the pinned files' contents.
	ConsultFiles
	// ConsultChat adds the conversation on top of the files.
	ConsultChat
)

func (s ConsultScope) String() string {
	switch s {
	case ConsultFiles:
		return "files"
	case ConsultChat:
		return "chat"
	default:
		return "none"
	}
}

// ParseConsultScope reads a scope name, reporting whether it is one.
func ParseConsultScope(name string) (ConsultScope, bool) {
	switch name {
	case "none":
		return ConsultNothing, true
	case "files":
		return ConsultFiles, true
	case "chat":
		return ConsultChat, true
	}
	return 0, false
}

// ConsultScopeNames lists the scopes in ladder order, for help text and errors.
var ConsultScopeNames = []string{"none", "files", "chat"}

// The advisor gets no system prompt and no tools, so whatever the material is
// has to be said in the one user message. These prefixes do that, and they are
// written for a reader who has no idea what Strument is.
const (
	consultFilesPrefix = "These files are pinned in an editing session. " +
		"They are here as context for the question at the end; you are being asked for an opinion, not to change them.\n"
	consultChatPrefix = "This is the session so far, between the user and the assistant they are working with.\n\n"
	consultChatSuffix = "\nThe user's question follows.\n"
)

// RunConsult asks another model a question outside the chat and returns its
// answer. It is /btw's side call with two additions: the advisor may be a
// different model, and it may be shown some of the session.
//
// The client and model are swapped in for the call, which is what makes the
// usage row land under the *advisor's* slug and price rather than the session
// model's. That matters more than it looks: a second opinion the ledger cannot
// see is exactly what a server-side advisor tool would have given us, and being
// able to read what the habit costs is most of the argument for having it here.
//
// Nothing is added to the conversation. The REPL asks first, and then adds the
// answer as material with a label, the way /run adds command output.
func (c *Coder) RunConsult(
	ctx context.Context,
	client llm.ModelClient,
	model *config.Model,
	question string,
	scope ConsultScope,
) string {
	if strings.TrimSpace(question) == "" {
		return ""
	}

	prompt := c.consultPrompt(question, scope)

	if client != nil && model != nil {
		defer func(prevClient llm.ModelClient, prevModel *config.Model) {
			c.Client, c.Model = prevClient, prevModel
		}(c.Client, c.Model)
		c.Client, c.Model = client, model
	}
	return c.runSide(ctx, prompt)
}

// consultPrompt assembles the one user message the advisor sees. The material
// comes first and the question last, so the question is what the model reads
// most recently — the same reason the system reminder stopped being repeated at
// the end of every send.
func (c *Coder) consultPrompt(question string, scope ConsultScope) string {
	var b strings.Builder

	if scope >= ConsultChat {
		b.WriteString(consultChatPrefix)
		// ViewContext is the fold as the model reads it, and it is already the
		// answer to "what is in this conversation" that /context prints. Using
		// it means there is one renderer rather than two that drift.
		b.WriteString(strings.TrimRight(c.ViewContext(-1), "\n"))
		b.WriteString("\n")
		b.WriteString(consultChatSuffix)
		b.WriteString("\n")
	}

	if scope >= ConsultFiles {
		if files := c.pinnedFilesBlock(); files != "" {
			b.WriteString(consultFilesPrefix)
			b.WriteString(files)
			b.WriteString("\n")
		}
	}

	b.WriteString(question)
	return b.String()
}

// pinnedFilesBlock renders the contents of every pinned file, editable and
// read-only alike, fenced and named the way the read-only block is.
//
// Both sets, because the advisor has no tools. In a normal turn the /add set is
// only *named* in the system prompt and the model reads what it needs — a change
// worth 383 blind edits (see pinnedFilesNote). An advisor cannot read anything,
// so a name would be an instruction it has no way to follow.
//
// It renders rather than calling readOnlyFilesContent for two reasons: that one
// covers a single set, and it writes the closing fence straight after the
// content, so a file with no trailing newline closes its fence mid-line. That is
// latent in the assembly path and would be a real hazard here, where a whole
// pinned tree can arrive at once.
func (c *Coder) pinnedFilesBlock() string {
	if len(c.absFnames) == 0 && len(c.absReadOnlyFnames) == 0 {
		return ""
	}
	// Picks a fence that none of the content collides with, and, as in a normal
	// turn, drops an unreadable pinned file from the chat with a warning.
	c.chooseFence()

	var b strings.Builder
	for _, fname := range append(append([]string(nil), c.absFnames...), c.absReadOnlyFnames...) {
		data, err := os.ReadFile(fname)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		b.WriteString("\n" + c.displayName(fname) + "\n")
		b.WriteString(c.fence.open + "\n")
		b.WriteString(content)
		b.WriteString(c.fence.close + "\n")
	}
	return b.String()
}

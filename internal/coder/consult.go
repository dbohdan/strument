package coder

import (
	"context"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// ConsultScope says how much of the session an advisor model is shown.
//
// The ladder exists because the right answer is not obvious and is measurable:
// an advisor that has read nothing gives generic advice, and an advisor that has
// read the whole transcript inherits the framing the user was trying to escape.
// doc/experiments/2026-09-consult/README.md is the trial that would settle the
// default. There was a middle rung, the pinned files' contents, which went with
// pinning; it was the default, and with nothing pinned it sent what "none" does.
type ConsultScope int

const (
	// ConsultNothing sends the question alone. This is what /btw does.
	ConsultNothing ConsultScope = iota
	// ConsultChat adds the conversation.
	ConsultChat
)

func (s ConsultScope) String() string {
	switch s {
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
	case "chat":
		return ConsultChat, true
	}
	return 0, false
}

// ConsultScopeNames lists the scopes in ladder order, for help text and errors.
var ConsultScopeNames = []string{"none", "chat"}

// The advisor gets no system prompt and no tools, so whatever the material is
// has to be said in the one user message. These prefixes do that, and they are
// written for a reader who has no idea what Strument is.
const (
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
		b.WriteString(strings.TrimRight(c.ViewContext(), "\n"))
		b.WriteString("\n")
		b.WriteString(consultChatSuffix)
		b.WriteString("\n")
	}

	b.WriteString(question)
	return b.String()
}

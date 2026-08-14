package coder

import (
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// commitMessageTool lets the model write the commit message itself, in the flow
// of the work, instead of the harness making a second request afterwards to ask
// a model to read the turn back and summarize it.
//
// Named commit_message rather than commit because it does not commit. The
// harness still does that, once, at the turn boundary — which is the review
// surface and stays the human's. A tool called "commit" would promise that
// calling it twice makes two commits, and it does not: the last message wins.
func commitMessageTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolCommitMessage,
		Description: "Set the commit message for the changes you are making this turn. " +
			"Call it once, after your last change, when you have edited any file. " +
			"The harness commits at the end of the turn and uses what you set here.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subject": strProp("One line: \"type(scope): description\", e.g. " +
					"\"fix(workspace): stop counting cache writes twice\". Use feat for a new " +
					"capability and fix for a bug; build, chore, ci, docs, perf, refactor, style, " +
					"and test are also conventional. The scope names the part of the codebase the " +
					"change is confined to — leave it out when the change spans several. " +
					"Imperative mood, no trailing period, under 72 characters. Put \"!\" before " +
					"the colon if the change breaks existing behavior."),
				"body": strProp("Optional, and usually empty. Add one only for something the diff " +
					"cannot say: why this approach, what was rejected, the constraint or " +
					"measurement behind the choice. The diff already says what changed, so a body " +
					"that restates it is noise. If the change breaks existing behavior, include a " +
					"paragraph starting \"BREAKING CHANGE: \" saying what breaks."),
			},
			"required": []any{"subject"},
		},
	}
}

// runCommitMessage records the message for the turn's commit.
//
// Last call wins. A turn that edits, runs the tests, finds something, and
// revises its own description should commit the revision — freezing the first
// one would keep the least informed version on purpose.
func (c *Coder) runCommitMessage(tc llm.ToolCall) string {
	var a struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	subject := strings.TrimSpace(a.Subject)
	if subject == "" {
		return "The required \"subject\" argument was missing."
	}
	// A subject with a newline in it is a model packing the whole message into
	// one field. Split rather than refuse: the intent is unmistakable, and a
	// check that refuses is a check that has to be right.
	body := strings.TrimSpace(a.Body)
	if subject, rest, found := strings.Cut(subject, "\n"); found {
		if body == "" {
			body = strings.TrimSpace(rest)
		}
		a.Subject = subject
	}
	subject = strings.TrimSpace(a.Subject)

	replaced := c.turnCommitMessage != ""
	c.turnCommitMessage = subject
	if body != "" {
		c.turnCommitMessage += "\n\n" + body
	}

	verb := "Commit message set"
	if replaced {
		verb = "Commit message replaced"
	}
	c.Out.Toolf("%s: %s", verb, subject)
	return "Noted. The turn's commit will use it."
}

// fallbackCommitMessage is what the commit says when the model did not call the
// tool. It is the first sentence of the model's own closing prose, which is an
// account of the turn it has already written and the user has already read.
//
// No invented type prefix. Guessing "chore" on what is really a feat is worse
// than saying nothing, because tooling reads the type and semver hangs off it —
// and the missing prefix is useful signal, since a subject without one is
// visibly a commit where the tool went uncalled.
func fallbackCommitMessage(answer string) string {
	text := strings.TrimSpace(answer)
	if text == "" {
		return ""
	}
	// The first line usually is the summary; within it, the first sentence.
	line, _, _ := strings.Cut(text, "\n")
	line = strings.TrimSpace(line)
	if s, _, found := strings.Cut(line, ". "); found {
		line = s + "."
	}
	const maxSubject = 72
	if len(line) > maxSubject {
		cut := strings.LastIndex(line[:maxSubject], " ")
		if cut <= 0 {
			cut = maxSubject
		}
		line = strings.TrimSpace(line[:cut]) + "…"
	}
	return line
}

// preparedCommitMessage is what the turn's commit should say: what
// commit_message set, or failing that the model's own closing prose.
//
// The fallback will run — models forget — so it has to be good rather than a
// placeholder. "(no commit message provided)" told the reader nothing; the
// closing prose is an account of the turn the model has already written and the
// user has already read on screen.
func (c *Coder) preparedCommitMessage() string {
	if c.turnCommitMessage != "" {
		return c.turnCommitMessage
	}
	return fallbackCommitMessage(c.partialResponseContent)
}

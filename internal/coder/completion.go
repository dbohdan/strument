package coder

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"dbohdan.com/strument/internal/llm"
)

// Tab-completion words drawn from the conversation.
//
// File paths were the only thing the main prompt completed. aider fills the
// gap by tokenizing every file in the chat, which suits a harness that sends
// whole files; this one sends names, and the model reads what it needs, so
// there is nothing comparable to tokenize. What there is instead is the
// conversation itself, and two parts of it say what is being worked on right
// now: the spans put in backticks, and the names the model went looking for.
//
// Recency is the relevance filter. Only the last few turns contribute, so a
// word drops out once nobody has mentioned it for a while; nothing has to rank
// the pool, because it never grows past what the current work is about.

// maxCompletionWord bounds a candidate. A longer backtick span is a sentence
// or an expression, not something anyone types a prefix of.
const maxCompletionWord = 64

var (
	fencedBlock = regexp.MustCompile("(?s)(```|~~~).*?(```|~~~)")
	inlineCode  = regexp.MustCompile("`([^`\n]+)`")
	// identifierLike is a grep pattern that is really a name: letters, digits
	// and the joiners identifiers and file names use, and no regex syntax.
	identifierLike = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
)

// CompletionWords returns the completion candidates the last turns of the
// conversation contribute, most recent first, without duplicates.
//
// Two sources. Inline code spans — `max_output`, `--no-git`, `formatWindow()` —
// from the model's answers and from what the user typed, where a span of one
// token is a name someone meant. And the arguments of the model's own lookups:
// the name it asked symbol for, and a grep pattern that is a plain identifier,
// which name what it went looking for whether or not it said so. Reasoning is
// not a source: it is often hidden, and it is where the model tries names it
// then rejects.
//
// A turn begins with a user message the harness did not write. Tool results
// and harness notes are user-role too and do not count.
func (c *Coder) CompletionWords(turns int) []string {
	msgs := make([]llm.Message, 0, len(c.doneMessages)+len(c.curMessages))
	msgs = append(msgs, c.doneMessages...)
	msgs = append(msgs, c.curMessages...)

	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}

	seenTurns := 0
	for i := len(msgs) - 1; i >= 0 && seenTurns < turns; i-- {
		m := msgs[i]
		switch m.Role {
		case llm.RoleUser:
			text := m.Text()
			if strings.HasPrefix(text, llm.HarnessMarker) {
				continue
			}
			for _, w := range codeSpanWords(text) {
				add(w)
			}
			seenTurns++
		case llm.RoleAssistant:
			for _, w := range codeSpanWords(m.Text()) {
				add(w)
			}
			for _, tc := range m.ToolCalls {
				add(lookupWord(tc))
			}
		}
	}
	return out
}

// codeSpanWords returns the one-token inline code spans in text. Fenced blocks
// are removed first: their contents are code to read, not names to type.
func codeSpanWords(text string) []string {
	text = fencedBlock.ReplaceAllString(text, "")
	var out []string
	for _, m := range inlineCode.FindAllStringSubmatch(text, -1) {
		if w := completionWord(m[1]); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// completionWord is span as a candidate, or "" when it is not one: more than
// one token, too long to be a name, or nothing a prefix could start. A call's
// trailing "()" is dropped, because what anyone types is the name.
func completionWord(span string) string {
	w := strings.TrimSuffix(strings.TrimSpace(span), "()")
	n := utf8.RuneCountInString(w)
	if n < 2 || n > maxCompletionWord || strings.IndexFunc(w, unicode.IsSpace) >= 0 {
		return ""
	}
	if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
		return ""
	}
	return w
}

// lookupWord is the name a symbol or grep call went looking for, or "".
func lookupWord(tc llm.ToolCall) string {
	var args struct {
		Name    string `json:"name"`
		Pattern string `json:"pattern"`
	}
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		return ""
	}
	switch tc.Name {
	case toolSymbol:
		return completionWord(args.Name)
	case toolGrep:
		if identifierLike.MatchString(args.Pattern) {
			return completionWord(args.Pattern)
		}
	}
	return ""
}

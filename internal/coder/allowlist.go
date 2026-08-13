package coder

import (
	"slices"
	"strings"

	"dbohdan.com/strument/internal/config"
	"mvdan.cc/sh/v3/syntax"
)

// matchConfiguredCheck reports which of the project's verify checks the command
// *is*, verbatim, and false when it is none of them.
//
// This is an allowlist, and the choice matters more than the mechanism. A
// blacklist — escalate on rm, curl, sudo — fails open: everything it did not
// think of sails through, and the misses are silent. An allowlist fails closed:
// the worst case is a prompt the user did not need, which they notice and can
// fix by naming the check in their config. Nothing here classifies a command as
// dangerous, because nothing here can.
//
// "Verbatim" is strict on purpose. The command must be a single simple command
// of bare literal words: no pipelines, no `;` or `&&`, no redirections, no
// backgrounding or negation, no leading assignments, and no expansions of any
// kind. Anything that could mean something other than what it says is not a
// match, and a non-match costs only the ordinary confirmation.
//
// Quoted words are rejected too, which is a real limitation rather than an
// oversight: a check configured as ["pytest", "-k", "not slow"] can never be
// typed in a form this accepts, so it always prompts. That is the direction to
// be wrong in, and it keeps this function free of unquoting — `$'…'` escapes and
// backslashes inside `"…"` are exactly where a matcher like this grows a hole.
func matchConfiguredCheck(command string, checks []config.VerifyCheck) (string, bool) {
	words, ok := literalWords(command)
	if !ok {
		return "", false
	}
	for _, ch := range checks {
		if slices.Equal(words, ch.Argv) {
			return ch.Name, true
		}
	}
	return "", false
}

// literalWords parses one shell command and returns its words when every one of
// them is a bare literal and the command carries nothing else. ok is false for
// anything more complicated, without saying which rule it broke: this is a
// yes/no gate, and a caller that acted on the reason would be classifying.
func literalWords(command string) ([]string, bool) {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	if len(file.Stmts) != 1 {
		return nil, false
	}
	stmt := file.Stmts[0]
	if len(stmt.Redirs) > 0 || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown {
		return nil, false
	}
	// Anything that is not a simple command — a pipeline, a subshell, an if, a
	// function — is a different Command implementation, so this one type
	// assertion covers all of them.
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return nil, false
	}

	words := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		if len(arg.Parts) != 1 {
			return nil, false
		}
		lit, ok := arg.Parts[0].(*syntax.Lit)
		if !ok {
			return nil, false
		}
		words = append(words, lit.Value)
	}
	return words, true
}

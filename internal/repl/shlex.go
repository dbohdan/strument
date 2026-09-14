package repl

import "dbohdan.com/strument/internal/shlex"

// splitArgs is internal/shlex's Split under the name the REPL's command
// handlers and the completion code have always called it, which is also the
// name their comments explain themselves in terms of.
//
// The implementation moved out when `strument config edit` needed the same rule
// to turn $VISUAL or $EDITOR into an argv: one splitter with a platform caveat
// worth getting right (a backslash escapes on Unix and is a path separator on
// Windows) is better than two that agree until one of them is edited.
func splitArgs(s string) []string { return shlex.Split(s) }

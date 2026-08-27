package repl

import "runtime"

// backslashEscapes reports whether an unquoted backslash escapes the next rune.
//
// It does on Unix, where that is the shell idiom `/add my\ file.txt` uses. It
// must not on Windows, where the backslash is the path separator: applying the
// Unix rule there turned `/read-only C:\Users\me\spec.md` into
// `C:Usersmespec.md`, which matched nothing and pinned nothing. Quoting still
// works on both, and is what a Windows user would reach for anyway.
//
// A variable rather than a runtime.GOOS test inside the loop, following
// envNamesFold: the platform this is wrong on is the one CI is least likely to
// exercise, so the other platform's rule has to be reachable from a test on any
// host. splitArgsWith is that seam.
var backslashEscapes = runtime.GOOS != "windows"

// splitArgs splits a command argument string into fields the way a shell would
// for the simple cases: whitespace separates fields, but quotes — and, on Unix,
// backslash escapes — let a field contain spaces. It is deliberately small —
// enough for file paths with spaces (`/add "my file.txt"` or, on Unix,
// `/add my\ file.txt`), not a full POSIX shell parser.
//
// Rules: outside quotes a backslash escapes the next rune where the platform
// says it does (see backslashEscapes); single quotes are literal (no escapes
// inside); double quotes are literal except that a backslash still escapes a
// `"` or another `\`. An unterminated quote runs to the end of the string.
func splitArgs(s string) []string {
	return splitArgsWith(s, backslashEscapes)
}

func splitArgsWith(s string, escapes bool) []string {
	var args []string
	var cur []rune
	inField := false
	rs := []rune(s)
	n := len(rs)

	flush := func() {
		if inField {
			args = append(args, string(cur))
			cur = cur[:0]
			inField = false
		}
	}

	for i := 0; i < n; {
		c := rs[i]
		switch {
		case c == '\\' && escapes && i+1 < n:
			cur = append(cur, rs[i+1])
			inField = true
			i += 2
		case c == '\'':
			inField = true
			i++
			for i < n && rs[i] != '\'' {
				cur = append(cur, rs[i])
				i++
			}
			i++ // skip the closing quote (or run off the end)
		case c == '"':
			inField = true
			i++
			for i < n && rs[i] != '"' {
				if rs[i] == '\\' && i+1 < n && (rs[i+1] == '"' || rs[i+1] == '\\') {
					cur = append(cur, rs[i+1])
					i += 2
					continue
				}
				cur = append(cur, rs[i])
				i++
			}
			i++
		case c == ' ' || c == '\t':
			flush()
			i++
		default:
			cur = append(cur, c)
			inField = true
			i++
		}
	}
	flush()
	return args
}

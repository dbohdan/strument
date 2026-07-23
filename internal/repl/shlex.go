package repl

// splitArgs splits a command argument string into fields the way a shell would
// for the simple cases: whitespace separates fields, but single quotes, double
// quotes, and backslash escapes let a field contain spaces. It is deliberately
// small — enough for file paths with spaces (`/add "my file.txt"` or
// `/add my\ file.txt`), not a full POSIX shell parser.
//
// Rules: outside quotes, a backslash escapes the next rune; single quotes are
// literal (no escapes inside); double quotes are literal except that a
// backslash still escapes a `"` or another `\`. An unterminated quote runs to
// the end of the string.
func splitArgs(s string) []string {
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
		case c == '\\' && i+1 < n:
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

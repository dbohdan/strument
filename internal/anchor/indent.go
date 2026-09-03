package anchor

import (
	"strconv"
	"strings"
)

// The indent codec: a line's leading whitespace, rendered as words.
//
//	"\t\t"        <->  "2 tabs"
//	"\t  "        <->  "1 tab 2 spaces"
//	""            <->  "0 spaces"
//
// It exists for one reason, and it is not tokens: phase 0 measured any run of
// whitespace as a single token, so naming it always costs more than the
// whitespace does. It is the safety net that anchored editing removes.
//
// Under a quoted-span edit the line matcher repairs whitespace drift — the
// model sends four spaces where the file has a tab, and the harness fixes it.
// Anchored editing has no matching at all, so that repair is gone while the
// error is not: phase 1 measured 30 of 72 outputs coming back misindented
// (doc/experiments/2026-09-anchored-edit-phase1.md). With the column the model
// never types indentation; it names it, and a name that does not parse is
// refused rather than written.
//
// Parsing is strict on purpose. "1 tabs" is rejected, and so is "2 tab": a
// model that cannot get the agreement right is a model whose indentation claim
// should not be trusted, and refusing costs a retry where accepting costs a
// wrong file.

// EncodeIndent renders a leading whitespace run as words. The empty run is
// "0 spaces" rather than "", so the column is always present and every row has
// the same shape.
func EncodeIndent(run string) string {
	if run == "" {
		return "0 spaces"
	}
	var parts []string
	for i := 0; i < len(run); {
		ch := run[i]
		n := 0
		for i < len(run) && run[i] == ch {
			n++
			i++
		}
		unit := "space"
		if ch == '\t' {
			unit = "tab"
		}
		if n != 1 {
			unit += "s"
		}
		parts = append(parts, strconv.Itoa(n)+" "+unit)
	}
	return strings.Join(parts, " ")
}

// ParseIndent is the inverse, on the runs EncodeIndent produces. It reports
// false for anything else — a count that is not a number, a unit that is not
// tab or space, or a count and unit that disagree on plurality.
func ParseIndent(s string) (string, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields)%2 != 0 {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(fields); i += 2 {
		n, err := strconv.Atoi(fields[i])
		if err != nil || n < 0 || n > 512 {
			return "", false
		}
		var ch byte
		switch fields[i+1] {
		case "tab":
			ch = '\t'
			if n != 1 {
				return "", false
			}
		case "tabs":
			ch = '\t'
			if n == 1 {
				return "", false
			}
		case "space":
			ch = ' '
			if n != 1 {
				return "", false
			}
		case "spaces":
			ch = ' '
			if n == 1 {
				return "", false
			}
		default:
			return "", false
		}
		b.WriteString(strings.Repeat(string(ch), n))
	}
	return b.String(), true
}

// SplitIndent separates a line's leading whitespace run from the rest.
func SplitIndent(line string) (run, rest string) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i], line[i:]
}

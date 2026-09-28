package editblock

import (
	"fmt"
	"strings"
	"unicode"
)

// ExplainLoose says how a search the line matcher accepted differed from the
// file, in a sentence addressed to the model that sent it. It is called only
// after DoReplace reported MatchLines, and works out afterwards which rung of
// the ladder took the edit rather than threading a verdict through it: the
// ladder is a port kept close to aider's, and this is commentary on it.
//
// The point is the next edit. A loose match that reports plain success teaches
// nothing, and the same miscount comes back: in a GPT-6 Luna run, four of six
// loose matches repeated one the harness had silently corrected a step or two
// before — a tab too many in a Go file, three too few in a deep templ one.
// Naming both indentations, and the line, gives the model the number it got
// wrong.
func ExplainLoose(content, search string) string {
	_, whole := prep(content)
	_, part := prep(search)
	if len(part) > 2 && strings.TrimSpace(part[0]) == "" {
		if s := explainIndent(whole, part[1:]); s != "" {
			return s
		}
	}
	if s := explainIndent(whole, part); s != "" {
		return s
	}
	if dotsRe.MatchString(search) {
		return `The "..." lines were read as standing for unchanged text between the parts you sent.`
	}
	return "The text you sent differs from the file in whitespace, so the closest lines were chosen."
}

// explainIndent finds where part matches whole but for leading whitespace and
// describes the first line whose indentation differs. "" when part does not
// match that way, or matches with no indentation difference at all.
func explainIndent(whole, part []string) string {
	trim := func(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }
	n := len(part)
	for i := 0; i+n <= len(whole); i++ {
		matched := true
		for k := range n {
			if trim(whole[i+k]) != trim(part[k]) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		for k := range n {
			if strings.TrimSpace(part[k]) == "" {
				continue
			}
			sent, file := leadingSpace(part[k]), leadingSpace(whole[i+k])
			if sent == file {
				continue
			}
			return fmt.Sprintf("Your lines were indented differently from the file's: line %d has %s, "+
				"and you sent %s. The edit was re-indented to fit; use the file's indentation next time.",
				i+k+1, describeIndent(file), describeIndent(sent))
		}
		return ""
	}
	return ""
}

func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))]
}

// describeIndent names an indentation the way a person counts it: "3 tabs",
// "8 spaces", "no indentation", or the characters themselves when mixed.
func describeIndent(s string) string {
	switch {
	case s == "":
		return "no indentation"
	case strings.Trim(s, "\t") == "":
		return plural(len(s), "tab", "tabs")
	case strings.Trim(s, " ") == "":
		return plural(len(s), "space", "spaces")
	}
	return fmt.Sprintf("%q", s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

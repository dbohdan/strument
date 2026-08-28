// Package skill reads Agent Skills SKILL.md files: a directory with YAML
// frontmatter and Markdown instructions, the format Claude Code, OpenCode,
// Codex, Kimi and others converged on.
//
// The format is the portable part. Everything past it — how skills are
// discovered, who may invoke one, whether one runs in a subagent, whether it
// can come from a remote provider — is where those harnesses diverge, and none
// of it belongs here.
package skill

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// This file is a parser for SKILL.md frontmatter. It is deliberately not a
// YAML parser, and will not become one.
//
// The grammar below is the whole language:
//
//	frontmatter := "---\n" entry* "---\n"
//	entry       := key ":" ( inline | block )
//	inline      := plain | "'" sq "'" | '"' dq '"'
//	block       := ("|"|">") ("-"|"+")? "\n" indented-line*
//
// There is no nesting. That is the security property, and it is structural
// rather than a limit bolted onto something deeper: with nothing nested there
// is nothing to recurse on, so no input can drive stack depth or the
// alias-expansion blowup that general YAML parsers keep being patched for
// (CVE-2021-4235, GHSA-4fcp-jxh7-23x8, and successors).
//
// The grammar was cut against evidence rather than against the specification.
// Of 67 real SKILL.md files surveyed, every one used name and description, 34
// used license, 3 used compatibility, one used a block scalar, and *none* used
// metadata, allowed-tools, or any of the invocation-control fields various
// harnesses have added. So metadata — the sole nested structure in the format,
// and the only construct that would force indentation to carry meaning — is
// not implemented at all.

const (
	// MaxFrontmatterBytes bounds the frontmatter, not the file: the Markdown
	// body after the closing delimiter is never parsed, only carried.
	MaxFrontmatterBytes = 64 * 1024
	// MaxFrontmatterLines bounds work even when every line is a single byte.
	MaxFrontmatterLines = 4096
	// MaxScalarBytes bounds one value, so a block scalar cannot accumulate the
	// whole budget into a single field.
	MaxScalarBytes = 16 * 1024
)

// Frontmatter is the parsed metadata. Every value is a string; there are no
// booleans, numbers, nulls, timestamps, or collections, because none of them
// can express anything the format needs and each is a decoder to get wrong.
type Frontmatter struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	// AllowedTools is read so a real file does not fail to parse, and carried
	// so the *user* can be shown what a skill asks for before trusting it. It
	// grants nothing. A portable file is never the authority on what this
	// harness may do; that is what the permission flags are for, and a
	// markdown file that could pre-approve the shell would undo them in one
	// line. Other harnesses disagree with each other about this field's force,
	// which is reason enough not to give it any.
	AllowedTools string
}

// knownKeys are the only top-level keys accepted. An unknown key is an error
// rather than something ignored: a misspelled field in a file that changes how
// a model behaves should be visible, not silently inert.
var knownKeys = []string{"name", "description", "license", "compatibility", "allowed-tools"}

// Parse splits a SKILL.md into its frontmatter and its body.
//
// The body is returned untouched and unparsed. Errors carry a line number
// counted from the start of the file, because that is the number an editor
// shows.
func Parse(src string) (Frontmatter, string, error) {
	fm, body, startLine, err := extract(src)
	if err != nil {
		return Frontmatter{}, "", err
	}
	parsed, err := parseFrontmatter(fm, startLine)
	if err != nil {
		return Frontmatter{}, "", err
	}
	return parsed, body, nil
}

// extract finds the frontmatter between delimiters and returns it with the
// body and the line number the frontmatter starts on.
//
// The closing delimiter is found by scanning lines, never by searching for the
// byte sequence "---". A block scalar may contain a line of three hyphens, and
// a substring search would end the frontmatter in the middle of a string.
func extract(src string) (fm, body string, startLine int, err error) {
	// A BOM before the delimiter is common enough from Windows editors to be
	// worth stepping over rather than refusing.
	src = strings.TrimPrefix(src, "\ufeff")
	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		if r, okCRLF := strings.CutPrefix(src, "---\r\n"); okCRLF {
			rest = r
		} else {
			return "", "", 0, errors.New("frontmatter: missing opening delimiter (the file must start with ---)")
		}
	}

	var lines []string
	remainder := rest
	count := 0
	for remainder != "" {
		count++
		if count > MaxFrontmatterLines {
			return "", "", 0, fmt.Errorf("frontmatter: more than %d lines", MaxFrontmatterLines)
		}
		line, tail, hasMore := strings.Cut(remainder, "\n")
		line = strings.TrimSuffix(line, "\r")
		if isDelimiter(line) {
			joined := strings.Join(lines, "\n")
			if len(joined) > MaxFrontmatterBytes {
				return "", "", 0, fmt.Errorf("frontmatter: larger than %d bytes", MaxFrontmatterBytes)
			}
			return joined, tail, 2, nil
		}
		lines = append(lines, line)
		if len(remainder)-len(tail) == 0 && !hasMore {
			break
		}
		remainder = tail
		if !hasMore {
			break
		}
	}
	return "", "", 0, errors.New("frontmatter: missing closing delimiter (--- on its own line)")
}

// isDelimiter reports whether a line closes the frontmatter. Only an
// unindented, otherwise-empty "---" does; trailing spaces are tolerated
// because editors leave them and the intent is unambiguous.
func isDelimiter(line string) bool {
	return strings.TrimRight(line, " \t") == "---"
}

// parseFrontmatter walks the entries. Line-oriented and non-recursive: each
// iteration consumes either one inline entry or one block scalar, and the
// index only ever moves forward.
func parseFrontmatter(fm string, startLine int) (Frontmatter, error) {
	var out Frontmatter
	seen := map[string]bool{}
	lines := strings.Split(fm, "\n")

	for i := 0; i < len(lines); i++ {
		lineNo := startLine + i
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := rejectUnsupported(line, lineNo); err != nil {
			return Frontmatter{}, err
		}
		if line[0] == ' ' || line[0] == '\t' {
			// Indentation only ever belongs to a block scalar, which is
			// consumed by the entry that opened it. Reaching one here means
			// structure the grammar does not have.
			return Frontmatter{}, fmt.Errorf("frontmatter: line %d: unexpected indentation "+
				"(nested values are not supported)", lineNo)
		}

		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			return Frontmatter{}, fmt.Errorf("frontmatter: line %d: expected \"key: value\"", lineNo)
		}
		key = strings.TrimSpace(key)
		if !isKnownKey(key) {
			return Frontmatter{}, fmt.Errorf("frontmatter: line %d: unknown field %q (known fields: %s)",
				lineNo, key, strings.Join(knownKeys, ", "))
		}
		if seen[key] {
			// Rejected rather than last-wins, because a file with two names
			// has no single meaning and picking one silently is a decision the
			// author did not make.
			return Frontmatter{}, fmt.Errorf("frontmatter: line %d: duplicate field %q", lineNo, key)
		}
		seen[key] = true

		value := strings.TrimSpace(rest)
		if head, ok := blockHeader(value); ok {
			consumed, text, err := parseBlock(lines, i+1, head, startLine)
			if err != nil {
				return Frontmatter{}, err
			}
			i = consumed - 1
			value = text
		} else {
			var err error
			if value, err = parseInline(value, lineNo); err != nil {
				return Frontmatter{}, err
			}
		}
		if len(value) > MaxScalarBytes {
			return Frontmatter{}, fmt.Errorf("frontmatter: line %d: %q is longer than %d bytes",
				lineNo, key, MaxScalarBytes)
		}
		assign(&out, key, value)
	}
	return out, nil
}

func isKnownKey(key string) bool {
	return slices.Contains(knownKeys, key)
}

func assign(fm *Frontmatter, key, value string) {
	switch key {
	case "name":
		fm.Name = value
	case "description":
		fm.Description = value
	case "license":
		fm.License = value
	case "compatibility":
		fm.Compatibility = value
	case "allowed-tools":
		fm.AllowedTools = value
	}
}

// rejectUnsupported names YAML this grammar does not have, so a file using it
// gets a sentence about what is unsupported rather than a confusing complaint
// about the line's shape. Silently misreading such a line is the outcome worth
// avoiding: an anchor or a tag that parses as a plain string is a value the
// author did not write.
func rejectUnsupported(line string, lineNo int) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "%"):
		return fmt.Errorf("frontmatter: line %d: YAML directives are not supported", lineNo)
	case trimmed == "...":
		return fmt.Errorf("frontmatter: line %d: YAML document markers are not supported", lineNo)
	case strings.HasPrefix(trimmed, "- "), trimmed == "-":
		return fmt.Errorf("frontmatter: line %d: sequences are not supported "+
			"(allowed-tools is a space-separated string)", lineNo)
	case strings.HasPrefix(trimmed, "<<"):
		return fmt.Errorf("frontmatter: line %d: merge keys are not supported", lineNo)
	}
	return nil
}

// blockHeader recognises a block scalar introducer and its chomping mode.
func blockHeader(value string) (chomp byte, ok bool) {
	if value == "" || (value[0] != '|' && value[0] != '>') {
		return 0, false
	}
	style := value[0]
	switch value {
	case string(style):
		return 'c', true // clip: one trailing newline
	case string(style) + "-":
		return '-', true // strip
	case string(style) + "+":
		return '+', true // keep
	}
	return 0, false
}

// parseBlock reads an indented block scalar and returns the index one past it.
//
// Indentation comes from the first non-empty line and every later line must
// match it; YAML's full indentation-detection rules are not reproduced, and
// human-authored files do not need them. Folding for ">" turns single
// newlines into spaces and keeps blank lines as paragraph breaks.
func parseBlock(lines []string, start int, chomp byte, startLine int) (int, string, error) {
	style := byte('|')
	if start > 0 && start-1 < len(lines) {
		if v := strings.TrimSpace(lines[start-1]); strings.Contains(v, ">") {
			style = '>'
		}
	}

	indent := -1
	var content []string
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			content = append(content, "")
			continue
		}
		// Measured over spaces *and* tabs, so a tab-indented line is recognised
		// as indentation and refused by name. Trimming spaces alone gave a tab
		// a lead of zero, which ended the block and produced a generic
		// complaint about indentation from the caller — the tab-specific
		// message below was unreachable.
		lead := len(line) - len(strings.TrimLeft(line, " \t"))
		if strings.ContainsRune(line[:lead], '\t') {
			return 0, "", fmt.Errorf("frontmatter: line %d: tabs cannot be used for indentation",
				startLine+i)
		}
		if lead == 0 {
			break // back to column zero: the block ended
		}
		if indent < 0 {
			indent = lead
		}
		if lead < indent {
			break
		}
		content = append(content, line[indent:])
	}
	if indent < 0 {
		return i, "", nil // an empty block is an empty string, not an error
	}

	// Trailing blank lines belong to chomping, not to the content.
	end := len(content)
	for end > 0 && content[end-1] == "" {
		end--
	}
	kept := content[:end]
	trailing := len(content) - end

	var text string
	if style == '>' {
		text = fold(kept)
	} else {
		text = strings.Join(kept, "\n")
	}
	switch chomp {
	case '-': // strip
	case '+':
		text += strings.Repeat("\n", trailing+1)
	default: // clip
		if text != "" {
			text += "\n"
		}
	}
	return i, text, nil
}

// fold joins lines the way YAML's folded scalar does: a single newline becomes
// a space, a blank line becomes a real break.
func fold(lines []string) string {
	var b strings.Builder
	for i, line := range lines {
		switch {
		case i == 0:
			b.WriteString(line)
		case line == "":
			b.WriteString("\n")
		case lines[i-1] == "":
			b.WriteString(line)
		default:
			b.WriteString(" ")
			b.WriteString(line)
		}
	}
	return b.String()
}

// parseInline reads a plain, single-quoted, or double-quoted scalar.
func parseInline(value string, lineNo int) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value[0] {
	case '\'':
		return parseSingleQuoted(value, lineNo)
	case '"':
		return parseDoubleQuoted(value, lineNo)
	case '&', '*', '!':
		// Anchors, aliases and tags. Named individually because each would
		// otherwise parse as an ordinary string and mean something else.
		return "", fmt.Errorf("frontmatter: line %d: anchors, aliases and tags are not supported", lineNo)
	case '{', '[':
		return "", fmt.Errorf("frontmatter: line %d: flow collections are not supported", lineNo)
	}
	// A plain scalar runs to the end of the line. A comment is not stripped:
	// "description: use # for headings" means what it says, and guessing that
	// a "#" starts a comment would quietly truncate real values.
	return value, nil
}

func parseSingleQuoted(value string, lineNo int) (string, error) {
	if len(value) < 2 || !strings.HasSuffix(value, "'") {
		return "", fmt.Errorf("frontmatter: line %d: unterminated single-quoted value", lineNo)
	}
	inner := value[1 : len(value)-1]
	// '' is the only escape a single-quoted YAML scalar has.
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] != '\'' {
			b.WriteByte(inner[i])
			continue
		}
		if i+1 < len(inner) && inner[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		return "", fmt.Errorf("frontmatter: line %d: unescaped quote in single-quoted value "+
			"(write '' for a literal quote)", lineNo)
	}
	return b.String(), nil
}

func parseDoubleQuoted(value string, lineNo int) (string, error) {
	if len(value) < 2 || !strings.HasSuffix(value, "\"") {
		return "", fmt.Errorf("frontmatter: line %d: unterminated double-quoted value", lineNo)
	}
	inner := value[1 : len(value)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] != '\\' {
			b.WriteByte(inner[i])
			continue
		}
		i++
		if i >= len(inner) {
			return "", fmt.Errorf("frontmatter: line %d: value ends in a backslash", lineNo)
		}
		switch inner[i] {
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			// Refused rather than passed through. YAML's full escape
			// repertoire includes \x, \u and \U, and a parser that silently
			// dropped the backslash would turn A into "u0041".
			return "", fmt.Errorf("frontmatter: line %d: unsupported escape %q "+
				`(only \\, \", \n, \r and \t are supported)`, lineNo, `\`+string(inner[i]))
		}
	}
	return b.String(), nil
}

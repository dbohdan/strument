package editblock

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Edit is one SEARCH/REPLACE block. Search/Replace keep their raw line
// endings; markers are excluded.
type Edit struct {
	Path    string
	Search  string
	Replace string
}

// Block is one parsed item: either a suggested shell command block or an
// edit block, in response order.
type Block struct {
	IsShell bool
	Shell   string // shell block body (fences excluded)
	Edit    Edit
}

// Marker regexes, matched against the stripped line (editblock-spec §1).
var (
	headPattern    = regexp.MustCompile(`^<{5,9} SEARCH>?\s*$`)
	dividerPattern = regexp.MustCompile(`^={5,9}\s*$`)
	updatedPattern = regexp.MustCompile(`^>{5,9} REPLACE\s*$`)
)

const (
	dividerErr = "======="
	updatedErr = ">>>>>>> REPLACE"
)

const tripleBackticks = "```"

var shellStarts = []string{
	"```bash", "```sh", "```shell", "```cmd", "```batch", "```powershell",
	"```ps1", "```zsh", "```fish", "```ksh", "```csh", "```tcsh",
}

// splitLines splits keeping line endings, on the same boundary set as
// Python's str.splitlines (LF, CR, CRLF, VT, FF, FS, GS, RS, NEL, LS, PS).
// Only \n and \r\n occur in practice; the rest is insurance for parity
// with the transliterated oracle.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		isSep := false
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			isSep = true
		case '\r':
			isSep = true
			if i+1 < len(s) && s[i+1] == '\n' {
				size = 2
			}
		}
		i += size
		if isSep {
			lines = append(lines, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

var missingFilenameErr = "Bad/missing filename. The filename must be alone on the line before the opening fence %s"

// FindBlocks ports find_original_update_blocks: it scans an LLM response
// for shell blocks and SEARCH/REPLACE blocks. A malformed block returns an
// error whose message embeds everything processed so far plus the cause —
// that text goes straight back to the model (editblock-spec §2).
func FindBlocks(content string, fence Fence, validFnames []string) ([]Block, error) {
	lines := splitLines(content)
	i := 0
	currentFilename := ""

	var blocks []Block
	for i < len(lines) {
		line := lines[i]
		stripped := strings.TrimSpace(line)

		// Shell blocks — unless one of the next two lines opens an edit
		// block (a shell-fenced edit block).
		nextIsEditblock := (i+1 < len(lines) && headPattern.MatchString(strings.TrimSpace(lines[i+1]))) ||
			(i+2 < len(lines) && headPattern.MatchString(strings.TrimSpace(lines[i+2])))
		isShellStart := false
		for _, start := range shellStarts {
			if strings.HasPrefix(stripped, start) {
				isShellStart = true
				break
			}
		}
		if isShellStart && !nextIsEditblock {
			var shellContent []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), tripleBackticks) {
				shellContent = append(shellContent, lines[i])
				i++
			}
			if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), tripleBackticks) {
				i++ // skip the closing fence
			}
			blocks = append(blocks, Block{IsShell: true, Shell: strings.Join(shellContent, "")})
			continue
		}

		// SEARCH/REPLACE blocks.
		if headPattern.MatchString(stripped) {
			block, newI, err := parseEditBlock(lines, i, fence, validFnames, &currentFilename)
			if err != nil {
				processed := strings.Join(lines[:min(newI+1, len(lines))], "")
				return nil, fmt.Errorf("%s\n^^^ %s", processed, err.Error())
			}
			blocks = append(blocks, Block{Edit: block})
			i = newI
		}

		i++
	}
	return blocks, nil
}

// parseEditBlock consumes one SEARCH/REPLACE block starting at the HEAD
// line index i. It returns the edit and the index of the block's terminator
// line (the caller's loop increment steps past it).
func parseEditBlock(lines []string, i int, fence Fence, validFnames []string, currentFilename *string) (Edit, int, error) {
	var filename string
	preceding := lines[max(0, i-3):i]
	// New-file idiom: HEAD immediately followed by DIVIDER resolves the
	// filename without restricting to in-chat files.
	if i+1 < len(lines) && dividerPattern.MatchString(strings.TrimSpace(lines[i+1])) {
		filename = FindFilename(preceding, fence, nil)
	} else {
		filename = FindFilename(preceding, fence, validFnames)
	}

	if filename == "" {
		if *currentFilename != "" {
			filename = *currentFilename
		} else {
			return Edit{}, i, fmt.Errorf(missingFilenameErr, fence.Open)
		}
	}
	*currentFilename = filename

	var originalText []string
	i++
	for i < len(lines) && !dividerPattern.MatchString(strings.TrimSpace(lines[i])) {
		originalText = append(originalText, lines[i])
		i++
	}
	if i >= len(lines) || !dividerPattern.MatchString(strings.TrimSpace(lines[i])) {
		return Edit{}, i, fmt.Errorf("Expected `%s`", dividerErr)
	}

	var updatedText []string
	i++
	for i < len(lines) && !(updatedPattern.MatchString(strings.TrimSpace(lines[i])) ||
		dividerPattern.MatchString(strings.TrimSpace(lines[i]))) {
		updatedText = append(updatedText, lines[i])
		i++
	}
	if i >= len(lines) || !(updatedPattern.MatchString(strings.TrimSpace(lines[i])) ||
		dividerPattern.MatchString(strings.TrimSpace(lines[i]))) {
		return Edit{}, i, fmt.Errorf("Expected `%s` or `%s`", updatedErr, dividerErr)
	}

	return Edit{
		Path:    filename,
		Search:  strings.Join(originalText, ""),
		Replace: strings.Join(updatedText, ""),
	}, i, nil
}

// StripFilename ports strip_filename: extract a filename candidate from a
// line, or "" (editblock-spec §3.1).
func StripFilename(line string, fence Fence) string {
	filename := strings.TrimSpace(line)

	if filename == "..." {
		return ""
	}

	if strings.HasPrefix(filename, fence.Open) {
		candidate := filename[len(fence.Open):]
		if candidate != "" && (strings.Contains(candidate, ".") || strings.Contains(candidate, "/")) {
			return candidate
		}
		return ""
	}

	if strings.HasPrefix(filename, tripleBackticks) {
		candidate := filename[len(tripleBackticks):]
		if candidate != "" && (strings.Contains(candidate, ".") || strings.Contains(candidate, "/")) {
			return candidate
		}
		return ""
	}

	filename = strings.TrimRight(filename, ":")
	filename = strings.TrimLeft(filename, "#")
	filename = strings.TrimSpace(filename)
	filename = strings.Trim(filename, "`")
	filename = strings.Trim(filename, "*")

	return filename
}

// FindFilename ports find_filename: scan the (up to 3) preceding lines,
// nearest first, hopping over fence-opening lines, then pick the best
// candidate (editblock-spec §3). Lines are raw (may keep line endings).
func FindFilename(lines []string, fence Fence, validFnames []string) string {
	// Reverse (nearest first) and keep at most 3.
	rev := make([]string, 0, 3)
	for k := len(lines) - 1; k >= 0 && len(rev) < 3; k-- {
		rev = append(rev, lines[k])
	}

	var filenames []string
	for _, line := range rev {
		if fn := StripFilename(line, fence); fn != "" {
			filenames = append(filenames, fn)
		}
		// Only continue as long as we keep seeing fences.
		if !strings.HasPrefix(line, fence.Open) && !strings.HasPrefix(line, tripleBackticks) {
			break
		}
	}

	if len(filenames) == 0 {
		return ""
	}

	// Exact match first.
	for _, fn := range filenames {
		for _, vfn := range validFnames {
			if fn == vfn {
				return fn
			}
		}
	}

	// Basename match.
	for _, fn := range filenames {
		for _, vfn := range validFnames {
			if fn == pathBase(vfn) {
				return vfn
			}
		}
	}

	// Fuzzy match at cutoff 0.8.
	for _, fn := range filenames {
		if m, ok := getCloseMatches1(fn, validFnames, 0.8); ok {
			return m
		}
	}

	// A candidate that looks like a filename with an extension.
	for _, fn := range filenames {
		if strings.Contains(fn, ".") {
			return fn
		}
	}

	return filenames[0]
}

// pathBase is Python's Path(p).name: the final component, treating both
// separators (pathlib on POSIX only splits on "/", but valid_fnames are
// repo-relative forward-slashed paths, so path.Base fits).
func pathBase(p string) string {
	return path.Base(strings.ReplaceAll(p, "\\", "/"))
}

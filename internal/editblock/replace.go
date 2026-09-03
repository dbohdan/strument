package editblock

import (
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// prep ensures a trailing newline and splits keeping line endings.
func prep(content string) (string, []string) {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, splitLines(content)
}

// perfectReplace slides a window of len(partLines) over wholeLines and
// splices replaceLines at the first exact (whitespace-inclusive) match.
// perfectReplace requires the run of lines to occur exactly once.
//
// It used to take the first. That is the failure the caller's uniqueness check
// was added to prevent — a harness reporting success on an underconstrained
// transformation — and the check could not see this path: it counts occurrences
// of the *raw* search text, which is zero precisely when the model's whitespace
// differs, so a search matching three identical blocks arrived here counted as
// "not ambiguous" and edited whichever came first. Measured doing exactly that
// in doc/experiments/2026-09-anchored-edit-m1.md: two runs silently rewrote the
// wrong function and were told they had succeeded.
func perfectReplace(wholeLines, partLines, replaceLines []string) (string, bool, bool) {
	n := len(partLines)
	found := -1
	for i := 0; i+n <= len(wholeLines); i++ {
		matched := true
		for k := range n {
			if wholeLines[i+k] != partLines[k] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		if found >= 0 {
			return "", true, false // ambiguous
		}
		found = i
	}
	if found < 0 {
		return "", false, false
	}
	return spliceLines(wholeLines, found, n, replaceLines), false, true
}

// spliceLines rebuilds the file with replaceLines substituted for the n lines
// at i.
func spliceLines(wholeLines []string, i, n int, replaceLines []string) string {
	var b strings.Builder
	for _, l := range wholeLines[:i] {
		b.WriteString(l)
	}
	for _, l := range replaceLines {
		b.WriteString(l)
	}
	for _, l := range wholeLines[i+n:] {
		b.WriteString(l)
	}
	return b.String()
}

func perfectOrWhitespace(wholeLines, partLines, replaceLines []string) (res string, ambiguous, ok bool) {
	if res, ambiguous, ok := perfectReplace(wholeLines, partLines, replaceLines); ok || ambiguous {
		return res, ambiguous, ok
	}
	return replacePartWithMissingLeadingWhitespace(wholeLines, partLines, replaceLines)
}

// ReplaceMostSimilarChunk is the matching ladder:
// perfect match, uniform-leading-whitespace match, the two again without a
// spurious leading blank line, then "..." elision. The upstream fuzzy step
// is dead code and is not ported.
func ReplaceMostSimilarChunk(whole, part, replace string) (res string, ambiguous, ok bool) {
	whole, wholeLines := prep(whole)
	part, partLines := prep(part)
	replace, replaceLines := prep(replace)

	// An ambiguous result stops the ladder rather than falling through to the
	// next rung. Trying a looser matcher after a stricter one found several
	// candidates can only find more, and answering from it would be the coin
	// flip the ambiguity check exists to refuse.
	if res, amb, ok := perfectOrWhitespace(wholeLines, partLines, replaceLines); ok || amb {
		return res, amb, ok
	}

	// Drop a spurious leading blank line (issue #25) and retry.
	if len(partLines) > 2 && strings.TrimSpace(partLines[0]) == "" {
		if res, amb, ok := perfectOrWhitespace(wholeLines, partLines[1:], replaceLines); ok || amb {
			return res, amb, ok
		}
	}

	// "..." elision. A malformed elision raises in Python and is treated as
	// no-match by the caller; here it is simply not-ok.
	if res, ok, err := tryDotDotDots(whole, part, replace); err == nil && ok {
		return res, false, true
	}

	return "", false, false
}

// leadingWhitespaceLen is len(p) - len(p.lstrip()) counted in runes.
func leadingWhitespaceLen(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			n++
		} else {
			break
		}
	}
	return n
}

func outdent(s string, n int) string {
	runes := []rune(s)
	if n > len(runes) {
		n = len(runes)
	}
	return string(runes[n:])
}

// replacePartWithMissingLeadingWhitespace handles the model omitting or
// truncating indentation uniformly across the block (ladder step 2).
func replacePartWithMissingLeadingWhitespace(wholeLines, partLines, replaceLines []string) (res string, ambiguous, ok bool) {
	// Outdent part and replace by the minimum leading whitespace over all
	// their non-blank lines.
	var leading []int
	for _, p := range partLines {
		if strings.TrimSpace(p) != "" {
			leading = append(leading, leadingWhitespaceLen(p))
		}
	}
	for _, p := range replaceLines {
		if strings.TrimSpace(p) != "" {
			leading = append(leading, leadingWhitespaceLen(p))
		}
	}
	minLeading := 0
	if len(leading) > 0 {
		minLeading = leading[0]
		for _, v := range leading[1:] {
			if v < minLeading {
				minLeading = v
			}
		}
	}
	if minLeading > 0 {
		out := make([]string, len(partLines))
		for i, p := range partLines {
			if strings.TrimSpace(p) != "" {
				out[i] = outdent(p, minLeading)
			} else {
				out[i] = p
			}
		}
		partLines = out
		out = make([]string, len(replaceLines))
		for i, p := range replaceLines {
			if strings.TrimSpace(p) != "" {
				out[i] = outdent(p, minLeading)
			} else {
				out[i] = p
			}
		}
		replaceLines = out
	}

	// Every position, not the first: see perfectReplace. A search whose
	// indentation is wrong matches every copy of a repeated block equally well,
	// and picking one of them is a coin flip made on the model's behalf.
	n := len(partLines)
	found, foundLeading := -1, ""
	for i := 0; i+n <= len(wholeLines); i++ {
		addLeading, ok := matchButForLeadingWhitespace(wholeLines[i:i+n], partLines)
		if !ok {
			continue
		}
		if found >= 0 {
			return "", true, false // ambiguous
		}
		found, foundLeading = i, addLeading
	}
	if found < 0 {
		return "", false, false
	}
	reindented := make([]string, len(replaceLines))
	for k, rline := range replaceLines {
		if strings.TrimSpace(rline) != "" {
			reindented[k] = foundLeading + rline
		} else {
			reindented[k] = rline
		}
	}
	return spliceLines(wholeLines, found, n, reindented), false, true
}

// matchButForLeadingWhitespace reports the single uniform indent prefix that
// maps partLines onto wholeLines, if there is exactly one.
func matchButForLeadingWhitespace(wholeLines, partLines []string) (string, bool) {
	num := len(wholeLines)
	for i := range num {
		if strings.TrimLeftFunc(wholeLines[i], unicode.IsSpace) != strings.TrimLeftFunc(partLines[i], unicode.IsSpace) {
			return "", false
		}
	}
	add := make(map[string]bool)
	var prefix string
	for i := range num {
		if strings.TrimSpace(wholeLines[i]) == "" {
			continue
		}
		w := []rune(wholeLines[i])
		p := []rune(partLines[i])
		// Python's whole[: len(whole)-len(part)]: a negative index slices
		// from the end.
		cut := len(w) - len(p)
		if cut < 0 {
			cut = max(0, len(w)+cut)
		}
		prefix = string(w[:cut])
		add[prefix] = true
	}
	if len(add) != 1 {
		return "", false
	}
	return prefix, true
}

var dotsRe = regexp.MustCompile(`(?m)^\s*\.\.\.\n`)

// splitKeepSeps splits s on dotsRe matches, returning the alternating
// [text, sep, text, sep, ..., text] list Python's re.split with a capturing
// group produces.
func splitKeepSeps(s string) []string {
	locs := dotsRe.FindAllStringIndex(s, -1)
	pieces := make([]string, 0, 2*len(locs)+1)
	prev := 0
	for _, loc := range locs {
		pieces = append(pieces, s[prev:loc[0]], s[loc[0]:loc[1]])
		prev = loc[1]
	}
	pieces = append(pieces, s[prev:])
	return pieces
}

// tryDotDotDots ports try_dotdotdots: elided edits where "..." lines mark
// unchanged spans. ok=false with err=nil means "no dots, step doesn't
// apply"; err != nil is the malformed case Python raises for.
func tryDotDotDots(whole, part, replace string) (string, bool, error) {
	partPieces := splitKeepSeps(part)
	replacePieces := splitKeepSeps(replace)

	if len(partPieces) != len(replacePieces) {
		return "", false, errUnpairedDots
	}
	if len(partPieces) == 1 {
		return "", false, nil // no dots in this edit block
	}

	// The separator strings at odd indices must be pairwise identical.
	for i := 1; i < len(partPieces); i += 2 {
		if partPieces[i] != replacePieces[i] {
			return "", false, errUnmatchedDots
		}
	}

	for i := 0; i < len(partPieces); i += 2 {
		p, r := partPieces[i], replacePieces[i]
		if p == "" && r == "" {
			continue
		}
		if p == "" && r != "" {
			if !strings.HasSuffix(whole, "\n") {
				whole += "\n"
			}
			whole += r
			continue
		}
		switch strings.Count(whole, p) {
		case 1:
			whole = strings.Replace(whole, p, r, 1)
		default:
			return "", false, errDotsChunkNotUnique
		}
	}
	return whole, true, nil
}

var (
	errUnpairedDots       = strError("Unpaired ... in SEARCH/REPLACE block")
	errUnmatchedDots      = strError("Unmatched ... in SEARCH/REPLACE block")
	errDotsChunkNotUnique = strError("dots chunk not found exactly once")
)

type strError string

func (e strError) Error() string { return string(e) }

// StripQuotedWrapping removes a redundant filename line and fence pair the
// model sometimes wraps around a section.
func StripQuotedWrapping(res, fname string, fence Fence) string {
	if res == "" {
		return res
	}

	lines := splitLinesNoEnds(res)

	if fname != "" && len(lines) > 0 && strings.HasSuffix(strings.TrimSpace(lines[0]), pathBase(fname)) {
		lines = lines[1:]
	}

	if len(lines) > 0 && strings.HasPrefix(lines[0], fence.Open) && strings.HasPrefix(lines[len(lines)-1], fence.Close) {
		if len(lines) <= 2 {
			lines = nil
		} else {
			lines = lines[1 : len(lines)-1]
		}
	}

	out := strings.Join(lines, "\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// splitLinesNoEnds is Python str.splitlines() without keepends: each line
// loses exactly its one trailing separator.
func splitLinesNoEnds(s string) []string {
	withEnds := splitLines(s)
	out := make([]string, len(withEnds))
	for i, l := range withEnds {
		if strings.HasSuffix(l, "\r\n") {
			out[i] = l[:len(l)-2]
			continue
		}
		r, size := utf8.DecodeLastRuneInString(l)
		switch r {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out[i] = l[:len(l)-size]
		default:
			out[i] = l
		}
	}
	return out
}

// DoReplace ports do_replace: strip wrapping, then create/append/replace.
// exists says whether the target file already exists;
// content is its current text ("" for a new file).
func DoReplace(fname string, content string, exists bool, beforeText, afterText string, fence Fence) (string, Match, bool) {
	// The arguments as the model wrote them. StripQuotedWrapping below is
	// aider's normalization for prose-parsed blocks: it drops a filename line
	// and a fence, and — the part that matters here — appends a trailing
	// newline to anything without one, because every line of a SEARCH block has
	// one. That coercion is what a substring match cannot survive, so the exact
	// path below uses the text untouched.
	rawBefore, rawAfter := beforeText, afterText

	beforeText = StripQuotedWrapping(beforeText, fname, fence)
	afterText = StripQuotedWrapping(afterText, fname, fence)

	if !exists && strings.TrimSpace(beforeText) == "" {
		// New file.
		content = ""
	} else if !exists {
		return "", MatchNone, false
	}

	if strings.TrimSpace(beforeText) == "" {
		// Append to existing file, or start a new file.
		return content + afterText, MatchAppend, true
	}

	// An exact substring, occurring once, is replaced as written.
	//
	// Everything below this line is aider's matcher, and aider's matcher is
	// line-oriented because a SEARCH/REPLACE block is: it splits both sides into
	// lines and looks for a run of them. That was the right shape for a text
	// format where the model transcribes whole lines between markers. It is the
	// wrong shape for a tool whose schema asks for "an exact span of text,
	// character for character" and says nothing about lines — and a model that
	// takes that at its word, sending `dialog clipping` to fix a typo mid-line,
	// got "the text to replace was not found" about text plainly in the file.
	// Found with MiMo-V2.5, which lost three of four edits that way and spent
	// the turn re-reading the file.
	//
	// Uniqueness is required, and that is stricter than what follows rather
	// than looser: the line matcher takes the first run that matches, while an
	// ambiguous substring here declines and asks for more context. A caller can
	// tell the two failures apart with CountOccurrences.
	if rawBefore != "" && strings.Count(content, rawBefore) == 1 {
		return strings.Replace(content, rawBefore, rawAfter, 1), MatchExact, true
	}

	newContent, ambiguous, ok := ReplaceMostSimilarChunk(content, beforeText, afterText)
	if ambiguous {
		return "", MatchAmbiguous, false
	}
	if !ok {
		return "", MatchNone, false
	}
	return newContent, MatchLines, true
}

// CountOccurrences reports how many times the search text appears verbatim in
// content, so a failed edit can say "three places, narrow it down" instead of
// "not found" — which is false and sends the model looking for a typo it did
// not make.
func CountOccurrences(content, search string) int {
	if search == "" {
		return 0
	}
	return strings.Count(content, search)
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

// pathBase is Python's Path(p).name: the final component, treating both
// separators (pathlib on POSIX only splits on "/", but valid_fnames are
// repo-relative forward-slashed paths, so path.Base fits).
func pathBase(p string) string {
	return path.Base(strings.ReplaceAll(p, "\\", "/"))
}

// Match names how DoReplace found the text it replaced. The distinction is
// worth carrying because the strategies are not equally trustworthy: an exact
// substring is the model saying what it meant, while everything below it is the
// harness guessing which lines were intended after the model transcribed them
// imperfectly. A caller that wants to report, count, or gate on that guessing
// needs to know it happened.
type Match int

const (
	// MatchNone: nothing matched and nothing was replaced.
	MatchNone Match = iota
	// MatchExact: the search text occurred verbatim, exactly once.
	MatchExact
	// MatchAppend: empty search text, so the replacement was appended or
	// started a new file. Not a guess, just not a search either.
	MatchAppend
	// MatchAmbiguous: the line matcher found the run of lines in more than one
	// place, so which one was meant is unknown and nothing was changed. Distinct
	// from MatchNone because the model's next move differs: not "look harder for
	// the text" but "say which of several identical places you mean".
	MatchAmbiguous
	// MatchLines: aider's line-oriented matcher found it — a perfect run of
	// lines, or one differing only in leading whitespace, or one reached after
	// dropping a spurious blank first line, or a "..." elision. This is the
	// fuzzy tier: the harness decided which lines the model meant.
	MatchLines
)

func (m Match) String() string {
	switch m {
	case MatchExact:
		return "exact"
	case MatchAppend:
		return "append"
	case MatchAmbiguous:
		return "ambiguous"
	case MatchLines:
		return "lines"
	default:
		return "none"
	}
}

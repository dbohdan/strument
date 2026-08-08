package coder

import (
	"fmt"

	"dbohdan.com/strument/internal/repomap"
)

// parseNote is the line to say when an edit has stopped a file parsing, and ""
// when there is nothing to say.
//
// It warns; it does not refuse. When this was planned there was no
// verify_auto — now the project's own checks run at turn end, and a compiler
// names a syntax error far better than tree-sitter's error recovery can. What
// the check still adds is immediacy: the model hears about it in the same step
// instead of at turn end. That survives without the power to reject, and a
// false positive then costs a sentence rather than refusing a correct edit —
// which matters, because grammars drift from the languages they describe.
//
// The judgement is one-sided by construction. It reports only a *regression*:
// a file that did not parse before, or that no grammar covers, says nothing,
// because the model may be mid-repair and a note it cannot act on is noise. And
// a clean parse is not a claim the file is right — a probe found this Python
// grammar accepting "def f(:" without complaint. Catching some breakage is
// worth having; claiming to catch all of it would not be true.
func parseNote(path, before, after string) string {
	wasClean, _, knownBefore := repomap.ParseStatus(path, []byte(before))
	nowClean, line, knownAfter := repomap.ParseStatus(path, []byte(after))
	if !knownBefore || !knownAfter || !wasClean || nowClean {
		return ""
	}
	// "near" is doing real work: tree-sitter recovers by wrapping a span in one
	// ERROR node, and that span can begin before the mistake does.
	return fmt.Sprintf(
		"%s parsed cleanly before this change and no longer does — the parser loses the thread near line %d. "+
			"Check it before moving on.", path, line)
}

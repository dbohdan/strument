package coder

import "strings"

// fenceFor is a Markdown code fence that text cannot close: backticks, one
// more than the longest run of them in text, and never fewer than three.
// CommonMark closes a fence only with a run at least as long as the one that
// opened it, so nothing inside can end the block early.
//
// It is how a tool result keeps what a command or a program printed apart from
// what Strument says. Output is anything at all, including text that reads like
// Strument — "The user chose not to run the command." — and only a boundary the
// output cannot reproduce makes "outside the fence" mean "the harness".
func fenceFor(text string) string {
	longest, run := 0, 0
	for i := range len(text) {
		if text[i] == '`' {
			run++
			longest = max(longest, run)
			continue
		}
		run = 0
	}
	return strings.Repeat("`", max(3, longest+1))
}

// fenced is text inside a fence from fenceFor, ending in a newline.
func fenced(text string) string {
	f := fenceFor(text)
	return f + "\n" + strings.TrimRight(text, "\n") + "\n" + f + "\n"
}

// fencedCapped is fenced with text capped to limit bytes first, so that the
// tool-result cap, which cuts the whole result, never cuts the closing fence
// and leaves the rest of the result inside the block. cut reports whether
// anything went, for the caller to say so outside the fence.
func fencedCapped(text string, limit int) (block string, cut bool) {
	if len(text) > limit {
		text, cut = text[:limit], true
	}
	return fenced(text), cut
}

// outputCutNote is said after a fence whose text fencedCapped shortened.
const outputCutNote = "The output above was cut short; it exceeded the size one tool result may carry."

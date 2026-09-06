package main

import (
	"fmt"
	"os"
	"strings"
)

// The harness's voice outside a running session.
//
// See doc/messages.md. The short version: `strument: ` marks harness voice
// where colour cannot. Inside the REPL, Theme.Tool's recessive grey already
// separates what Strument says from what the model says, which is why
// Output.Toolf carries no prefix; out here there is no such channel, so the
// prefix does that work.
//
// It lives in one place because it did not use to. Fifteen call sites spelled
// the prefix as a literal, config.Load's warn hook was never given an
// implementation so config warnings printed bare, and envset.go built the
// prefix into a string it returned for someone else to print — three renderings
// of one class of message, and the two untrusted-project warnings ended up
// looking like they came from different programs.
const noticePrefix = "strument: "

// notice prints one harness notice to stderr.
//
// Continuation lines indent two spaces rather than repeating the prefix, so a
// message reads as one message. Pass them as extra arguments; each is one line.
func noticef(format string, args ...any) {
	fmt.Fprint(os.Stderr, noticePrefix+fmt.Sprintf(format, args...)+"\n")
}

// noticeWith prints a notice and its continuation lines, indented under it.
//
//	strument: ignoring 1 untrusted project skill: release-notes
//	  Run `strument trust` in this directory to allow it.
func noticeWith(first string, rest ...string) {
	var b strings.Builder
	b.WriteString(noticePrefix)
	b.WriteString(first)
	b.WriteString("\n")
	for _, line := range rest {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	fmt.Fprint(os.Stderr, b.String())
}

// warnNoticef is config.Options.Warn: it renders a config warning as a notice
// like any other.
//
// Config warnings used to have no Warn at all, so they fell through to the
// package's default — a bare stderr write with no prefix — while the messages
// printed on either side of them carried one. A newline in the message starts a
// continuation line, which is how a two-part warning (the fact, then the
// remedy) reaches noticeWith without config needing to know that shape exists.
func warnNoticef(format string, args ...any) {
	lines := strings.Split(fmt.Sprintf(format, args...), "\n")
	noticeWith(lines[0], lines[1:]...)
}

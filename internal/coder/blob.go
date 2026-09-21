package coder

import "strings"

// Which payloads leave the record and go to the blob store.
//
// The record holds a turn's tool calls and their results verbatim, which is
// what makes it worth having and also what makes it a growing pile of whatever
// the model read out of the project. The blob store exists so that pile can be
// pruned without the timeline being forgotten (internal/history/blob.go); this
// file decides what goes into it.
//
// The rule is a size floor with one exception, rather than a table with a row
// per tool. A table was the plan, and writing it showed it would have had one
// meaningful row: every other tool's answer is machine payload whose only
// interesting property is how big it is.

// blobFloor is the payload size, in bytes, at or above which a payload is
// stored separately.
//
// A kilobyte is about where a tool result stops being something a reader scans
// in the record and starts being something they would page through. Below it
// the hash and the description cost more than the payload they replace: 64
// characters of hash plus a first line is most of 200 bytes.
//
// The floor is why `strument history edit` stays worth having. A secret that
// arrives in a small result is inline forever, because a sweep that deletes
// blobs cannot reach it — editing the record by hand is the answer for one
// specific thing, and pruning is the answer for bulk.
const blobFloor = 1024

// maxPayloadSummary caps the description left behind in a payload's place.
// The same 200 as maxToolLogLine, for the same reason: it is a line in
// something a person reads.
const maxPayloadSummary = 200

// inlineTools never have their results stored separately, whatever the size.
//
// ask_user_question's result is the user's own words. It is part of the
// conversation rather than machine payload, and a record whose human turns
// could go missing while the tool output stayed would have the fidelity
// backwards.
//
// interrupt is here because its result is one sentence the harness wrote and
// blobbing it could only ever cost bytes — the floor would keep it inline
// anyway, so this is documentation more than mechanism.
var inlineTools = map[string]bool{
	toolAskUser:   true,
	toolInterrupt: true,
}

// storeSeparately reports whether a payload from this tool goes to the blob
// store. An empty tool name is an unattributed payload, which gets the default
// rule: the only tool the name changes the answer for is one whose results are
// short anyway.
func storeSeparately(tool string, size int) bool {
	return !inlineTools[tool] && size >= blobFloor
}

// payloadSummary is what stands in for a payload that is no longer inline.
//
// The first line, because Strument's tools put a header there — "main.go (3
// lines)", "12 matches in 4 files" — and a tool call's arguments are one line
// of JSON whose front is the path, ordered that way on purpose (orderedProps
// in tools.go: "a whole file's worth of diff waited on the path that followed
// it"). One rule reads both.
//
// It does not try to say what the tool was or what it was called with. Both
// are already in the record, on the assistant message that made the call; a
// description that repeated them would be spending bytes to say something the
// line above already says.
func payloadSummary(payload string) string {
	line := payload
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if len(line) > maxPayloadSummary {
		// Cut on a rune boundary: the summary is rendered to a terminal, and
		// half a rune there is a replacement character rather than a hint.
		cut := maxPayloadSummary
		for cut > 0 && !isRuneStart(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	return line
}

// isRuneStart reports a byte that begins a UTF-8 rune.
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

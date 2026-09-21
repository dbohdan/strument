package history

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"dbohdan.com/strument/internal/coder"
)

// This file reads the session record back. The record is written by
// internal/jsonlog from internal/coder's Record; here it is turned into the
// Turns the markdown renderer above already knows how to print, so that the
// transcript becomes a view of the record rather than a second thing to keep
// true.
//
// The turn rows carry the prompt and the answer as the transcript received
// them, so nothing here reassembles a turn out of its messages. That was
// tried on paper and does not work: an interrupted send's content is
// accumulated with its steer, while a failed automatic check re-enters the
// loop as an ordinary user message whose reply *replaces* what came before,
// and the two are indistinguishable in the message stream. A renderer that
// guessed would show a sentence the transcript never did.

// TurnsFromRecords rebuilds the turns in a record stream, oldest first.
//
// A record that is not a turn is skipped rather than examined: everything the
// transcript shows is on the turn row. The message and reasoning rows are the
// conversation, which is a different question and has its own readers.
func TurnsFromRecords(records []coder.Record) []Turn {
	turns := make([]Turn, 0, len(records)/8)
	for _, r := range records {
		if r.Type != "turn" {
			continue
		}
		t := Turn{
			Model:          r.Model,
			TokensSent:     r.Sent,
			TokensReceived: r.Received,
			Cost:           r.Cost,
			CostKnown:      r.CostKnown,
			User:           r.Prompt,
			Assistant:      r.Answer,
			Files:          r.Files,
			Tools:          r.Tools,
			Crashed:        r.Outcome == coder.OutcomeCrashed,
		}
		// A row written before the record carried a clock, or by a build that
		// failed to format one, renders at the time it is read — which is what
		// render() does with a zero time anyway. Better a wrong second than a
		// header reading "0001-01-01".
		if ts, err := time.Parse(time.RFC3339, r.Time); err == nil {
			t.Time = ts
		}
		turns = append(turns, t)
	}
	return turns
}

// ReadRecords reads one JSONL segment.
//
// A line that does not decode ends the file rather than failing it. The
// writer flushes per record, so the only malformed line a real segment holds
// is a last one cut off when the process died — and a session that ended that
// way is exactly the one whose history someone wants to read.
func ReadRecords(path string) ([]coder.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []coder.Record
	sc := bufio.NewScanner(f)
	// A tool result can be large, and the default 64 KiB token would end a
	// segment at the first one that is not.
	sc.Buffer(make([]byte, 0, 64*1024), maxRecordLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r coder.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			break
		}
		out = append(out, r)
	}
	// A scanner error is the same case as an undecodable line: what was read
	// is what there is.
	return out, nil
}

// maxRecordLine caps one record. The tool results the log carries verbatim are
// already bounded by the harness; this is the backstop that keeps a corrupt
// file from being read into memory whole.
const maxRecordLine = 16 << 20

// ReadTurns rebuilds a session's turns from every segment it has, oldest
// first.
//
// Segments are concatenated rather than merged: LogSegments sorts by the
// timestamp in the name, one process wrote each, and the project lock keeps
// two from overlapping.
func ReadTurns(projectRoot, session string) ([]Turn, error) {
	segments, err := LogSegments(projectRoot, session)
	if err != nil {
		return nil, err
	}
	var turns []Turn
	for _, seg := range segments {
		records, err := ReadRecords(seg)
		if err != nil {
			// A segment that cannot be opened is skipped, not fatal: the rest
			// of the history is still worth showing.
			continue
		}
		turns = append(turns, TurnsFromRecords(records)...)
	}
	return turns, nil
}

// Markdown renders turns in the transcript's format, header and all.
//
// Byte-for-byte what Writer produced, including its skip rule: a turn with no
// answer, no tool lines and no changed files was never written, and a derived
// transcript that showed those turns would not be the same document.
func Markdown(turns []Turn) string {
	var b strings.Builder
	b.WriteString(transcriptTitle)
	for _, t := range turns {
		if skipTurn(t) {
			continue
		}
		b.WriteString(t.render())
	}
	return b.String()
}

// LastTurns returns the final n turns, or all of them when n is not positive.
// Counted after the skip rule, so `-t 5` shows five turns rather than five
// rows of which some are blank.
func LastTurns(turns []Turn, n int) []Turn {
	kept := make([]Turn, 0, len(turns))
	for _, t := range turns {
		if !skipTurn(t) {
			kept = append(kept, t)
		}
	}
	if n > 0 && n < len(kept) {
		kept = kept[len(kept)-n:]
	}
	return kept
}

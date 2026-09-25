package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/llm"
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
	out, _, err := readRecords(path)
	return out, err
}

// readRecords is ReadRecords that also says whether the segment was read to
// its end.
//
// Complete means nothing was skipped except a torn last line. A reader that
// only shows history can take a prefix; the strip sweep cannot, because it
// infers "nothing refers to this payload" from what it did not see, and a
// reference past an unreadable line is one it did not see.
func readRecords(path string) (out []coder.Record, complete bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// A tool result can be large, and the default 64 KiB token would end a
	// segment at the first one that is not.
	sc.Buffer(make([]byte, 0, 64*1024), maxRecordLine)
	stopped := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if stopped {
			// A row after the undecodable one: that was damage, not a tail.
			return out, false, nil
		}
		var r coder.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			stopped = true
			continue
		}
		out = append(out, r)
	}
	// A scanner error is the same case as an undecodable line for a reader —
	// what was read is what there is — but the rest of the file went unread.
	return out, sc.Err() == nil, nil
}

// maxRecordLine caps one record. The tool results the log carries verbatim are
// already bounded by the harness; this is the backstop that keeps a corrupt
// file from being read into memory whole.
const maxRecordLine = 16 << 20

// Resolve puts a record's payloads back where they were, reading the blob
// store, and reports whether every one it needed was there.
//
// A missing payload is not an error. Pruning is a supported operation — that
// is the whole reason the payloads are separable — so a record that outlives
// its blobs is the design working. What takes the payload's place is the
// description the record kept beside the hash, marked as a stand-in so that
// neither a reader nor a model replaying the conversation mistakes it for
// what the tool actually said.
//
// The hash stays on the record either way. A payload that comes back later —
// restored from a backup, or written again by a tool that read the same
// unchanged file — is found by the same name.
func Resolve(projectRoot string, r coder.Record) (coder.Record, bool) {
	whole := true
	// found reports the payload, or false when the blob is gone; blob empty
	// means the payload was inline all along and there is nothing to do.
	found := func(blob string) (string, bool) {
		if data, ok := GetBlob(projectRoot, blob); ok {
			return string(data), true
		}
		whole = false
		return "", false
	}
	if r.Blob != "" {
		text, ok := found(r.Blob)
		if !ok {
			what := "This message"
			if r.Role == "tool" {
				what = "This result"
			}
			text = strippedNote(what, r.Summary, r.Bytes)
		}
		r.Text = text
	}
	if len(r.ToolCalls) > 0 {
		// A copy, so resolving does not write into the caller's record.
		r.ToolCalls = slices.Clone(r.ToolCalls)
	}
	for i, tc := range r.ToolCalls {
		if tc.Blob == "" {
			continue
		}
		if args, ok := found(tc.Blob); ok {
			r.ToolCalls[i].Arguments = args
			continue
		}
		// Arguments are replayed as the call's JSON, and a provider refuses a
		// request carrying a call whose arguments do not parse — so the words
		// cannot go here the way they do for a result. An empty object stands
		// in, and the note rides along to be put in front of the call's result,
		// where the model reads it; without it the model saw a call that
		// apparently took no arguments and produced whatever it produced.
		r.ToolCalls[i].Arguments = "{}"
		r.ToolCalls[i].Pruned = strippedNote("This call's arguments", tc.Summary, tc.Bytes)
	}
	return r, whole
}

// strippedNote stands in for a payload that is gone. what names it as the
// sentence's subject: "This result", "This message", "This call's arguments".
//
// It says so in words rather than leaving a blank, because this text can be
// replayed to a model: a tool result that came back empty reads as a tool that
// found nothing, which is a different and wrong thing. Saying the payload is no
// longer stored is both true and something a model can reason about.
func strippedNote(what, summary string, size int) string {
	verb, pronoun := "is", "It"
	if strings.HasSuffix(what, "arguments") {
		verb, pronoun = "are", "They"
	}
	note := "[strument] " + what + " " + verb + " no longer stored."
	if size > 0 {
		note += fmt.Sprintf(" %s %s %d bytes.", pronoun, map[bool]string{true: "were", false: "was"}[pronoun == "They"], size)
	}
	if summary != "" {
		note += " " + pronoun + " began: " + summary
	}
	return note
}

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

// Runs lists a session's record segments that hold anything past the session
// header, oldest first: one per run that did something.
//
// Every run opens a segment, including one that was started and quit without
// a message, and that segment holds the header alone. Counting it made the
// newest segment an empty file after a run that did nothing, so "the latest
// run" meant the least interesting one. A run killed mid-turn still counts:
// its messages are there even though its turn record is not.
func Runs(projectRoot, session string) ([]string, error) {
	segments, err := LogSegments(projectRoot, session)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, seg := range segments {
		records, err := ReadRecords(seg)
		if err != nil {
			continue
		}
		for _, r := range records {
			if r.Type != "session" {
				out = append(out, seg)
				break
			}
		}
	}
	return out, nil
}

// RunSummary is one run of a session, as `strument history list` shows it.
type RunSummary struct {
	Path string
	// Started is when the run opened its segment, read from the file name.
	Started time.Time
	Turns   int
	// Prompt is the first thing typed in the run, and Model the model of its
	// last turn — the session header's when the run finished no turn.
	Prompt    string
	Model     string
	Cost      float64
	CostKnown bool
}

// RunSummaries describes the session's runs, oldest first, counting only the
// runs Runs counts, so an index into it is an index `--back` accepts.
func RunSummaries(projectRoot, session string) ([]RunSummary, error) {
	runs, err := Runs(projectRoot, session)
	if err != nil {
		return nil, err
	}
	out := make([]RunSummary, 0, len(runs))
	for _, seg := range runs {
		records, err := ReadRecords(seg)
		if err != nil {
			continue
		}
		s := RunSummary{Path: seg}
		s.Started, _ = time.Parse(logSegmentStamp, strings.TrimSuffix(filepath.Base(seg), ".jsonl"))
		for _, r := range records {
			switch {
			case r.Type == "session" && s.Model == "":
				s.Model = r.Model
			case r.Type == "turn":
				s.Turns++
				s.Model = r.Model
				s.Cost += r.Cost
				s.CostKnown = s.CostKnown || r.CostKnown
				if s.Prompt == "" {
					s.Prompt = r.Prompt
				}
			case r.Type == "message" && r.Role == "user" && s.Prompt == "" && s.Turns == 0 &&
				!strings.HasPrefix(r.Text, llm.HarnessMarker) && r.ToolCallID == "":
				// A run killed mid-turn has no turn row, but its prompt is
				// still the first message.
				s.Prompt = r.Text
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// ReadSessionRecords reads a session's whole record, oldest first, with every
// payload put back from the blob store.
//
// The second return is how many payloads were not there. Callers say so: a
// conversation restored with three tool results replaced by "this result is no
// longer stored" is still a conversation, but the difference between that and
// the real thing is exactly what someone pruning history should be able to
// see.
//
// Segments are concatenated for the same reason ReadTurns concatenates them:
// LogSegments sorts by the timestamp in the name, one process wrote each, and
// the project lock keeps two from overlapping.
func ReadSessionRecords(projectRoot, session string) ([]coder.Record, int, error) {
	segments, err := LogSegments(projectRoot, session)
	if err != nil {
		return nil, 0, err
	}
	var out []coder.Record
	missing := 0
	for _, seg := range segments {
		records, err := ReadRecords(seg)
		if err != nil {
			// A segment that cannot be opened is skipped, not fatal: the rest
			// of the conversation is still worth having.
			continue
		}
		for _, r := range records {
			resolved, whole := Resolve(projectRoot, r)
			if !whole {
				missing++
			}
			out = append(out, resolved)
		}
	}
	return out, missing, nil
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

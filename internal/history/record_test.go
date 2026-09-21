package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/coder"
)

// recordTurns is the fixture: one plain turn, one steered turn whose answer
// carries the blockquoted arc, one crashed turn, and one empty turn the
// transcript never wrote.
func recordTurns() ([]coder.Record, []Turn) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return ts
	}
	cost := 0.0005
	records := []coder.Record{
		{Type: "session", Version: coder.RecordVersion, Model: "flash"},
		{Type: "message", Role: "user", Text: "change the greeting"},
		{
			Type: "turn", Time: "2026-07-17T14:30:05Z", Model: "openrouter/flash",
			Outcome: "Success", Steps: 2, Sent: 2600, Received: 433,
			Cost: cost, CostKnown: true,
			Files:  []string{"greet.go"},
			Tools:  []string{"Read greet.go (5 lines)", "Edited greet.go"},
			Prompt: "change the greeting", Answer: "Sure, here is the change.",
		},
		{
			Type: "turn", Time: "2026-07-17T14:31:05Z", Model: "openrouter/flash",
			Outcome: "Success", Sent: 10, Received: 5,
			Tools:  []string{"Read greet.go (5 lines)"},
			Prompt: "and the farewell", Answer: "Starting on it.\n\n> no, the other one\n\nDone.",
		},
		{
			Type: "turn", Time: "2026-07-17T14:32:05Z", Model: "openrouter/flash",
			Outcome: coder.OutcomeCrashed, Sent: 7, Received: 1,
			Files:  []string{"greet.go"},
			Prompt: "again", Answer: "I was in the middle of",
		},
		{
			// No answer, no tools, no files: never written, so never derived.
			Type: "turn", Time: "2026-07-17T14:33:05Z", Model: "openrouter/flash",
			Outcome: "Failed", Prompt: "hello?",
		},
	}
	turns := []Turn{
		{
			Time: at("2026-07-17T14:30:05Z"), Model: "openrouter/flash",
			TokensSent: 2600, TokensReceived: 433, Cost: cost, CostKnown: true,
			User: "change the greeting", Assistant: "Sure, here is the change.",
			Files: []string{"greet.go"},
			Tools: []string{"Read greet.go (5 lines)", "Edited greet.go"},
		},
		{
			Time: at("2026-07-17T14:31:05Z"), Model: "openrouter/flash",
			TokensSent: 10, TokensReceived: 5,
			User:      "and the farewell",
			Assistant: "Starting on it.\n\n> no, the other one\n\nDone.",
			Tools:     []string{"Read greet.go (5 lines)"},
		},
		{
			Time: at("2026-07-17T14:32:05Z"), Model: "openrouter/flash",
			TokensSent: 7, TokensReceived: 1,
			User: "again", Assistant: "I was in the middle of",
			Files: []string{"greet.go"}, Crashed: true,
		},
		{Time: at("2026-07-17T14:33:05Z"), Model: "openrouter/flash", User: "hello?"},
	}
	return records, turns
}

// The point of the whole exercise: the document derived from the record is the
// document the transcript writer produced, byte for byte. Anything less and
// retiring the transcript loses something.
func TestDerivedMarkdownMatchesTheWrittenTranscript(t *testing.T) {
	records, turns := recordTurns()

	path := filepath.Join(t.TempDir(), "transcript.md")
	w := New(path)
	for _, turn := range turns {
		if err := w.Append(turn); err != nil {
			t.Fatal(err)
		}
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	derived := Markdown(TurnsFromRecords(records))
	if derived != string(written) {
		t.Errorf("derived transcript differs from the written one\n--- written ---\n%s\n--- derived ---\n%s",
			written, derived)
	}
	// The fixture has to reach both interesting branches, or the comparison
	// above is between two renderings of the plain case.
	if !strings.Contains(derived, "the turn crashed before finishing") {
		t.Error("fixture never exercised a crashed turn")
	}
	if strings.Contains(derived, "hello?") {
		t.Error("a turn with no answer and no work was derived; the writer drops those")
	}
}

// Segments are read oldest first and concatenated, which is what makes one
// file per process start a working rotation scheme rather than a pile.
func TestReadTurnsConcatenatesSegmentsOldestFirst(t *testing.T) {
	root := t.TempDir()
	records, _ := recordTurns()

	for i, when := range []time.Time{
		time.Date(2026, 7, 17, 14, 30, 0, 0, time.UTC),
		time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC),
	} {
		seg, err := NewLogSegment(root, DefaultSession, when)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		enc := json.NewEncoder(&b)
		for _, r := range records {
			if r.Type == "turn" {
				r.Prompt = r.Prompt + " (segment " + string(rune('A'+i)) + ")"
			}
			if err := enc.Encode(r); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(seg, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	turns, err := ReadTurns(root, DefaultSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 8 {
		t.Fatalf("got %d turns across two segments, want 8", len(turns))
	}
	if got := turns[0].User; !strings.HasSuffix(got, "(segment A)") {
		t.Errorf("first turn came from %q, want the older segment", got)
	}
	if got := turns[len(turns)-1].User; !strings.HasSuffix(got, "(segment B)") {
		t.Errorf("last turn came from %q, want the newer segment", got)
	}
}

// A session killed mid-write leaves a truncated last line. That session's
// history is exactly the one someone wants back, so the reader keeps what
// decoded and stops.
func TestReadRecordsKeepsWhatDecodedBeforeATruncatedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg.jsonl")
	body := `{"type":"session","model":"flash"}` + "\n" +
		`{"type":"turn","prompt":"one","answer":"done"}` + "\n" +
		`{"type":"turn","prompt":"two","answ`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := ReadRecords(path)
	if err != nil {
		t.Fatalf("a truncated tail is not an error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want the 2 that decoded", len(records))
	}
	if turns := TurnsFromRecords(records); len(turns) != 1 || turns[0].User != "one" {
		t.Errorf("turns = %+v, want the one complete turn", turns)
	}
}

// -t counts turns the reader will show, not rows in the file: a run whose
// last three rows include two empty turns would otherwise answer -t 3 with
// one turn.
func TestLastTurnsCountsAfterTheSkipRule(t *testing.T) {
	_, turns := recordTurns()
	got := LastTurns(turns, 2)
	if len(got) != 2 {
		t.Fatalf("got %d turns, want 2", len(got))
	}
	if got[1].User != "again" {
		t.Errorf("last turn is %q, want the crashed one — the empty turn should not count", got[1].User)
	}
	if n := len(LastTurns(turns, 0)); n != 3 {
		t.Errorf("no limit gave %d turns, want the 3 that are not skipped", n)
	}
}

package loopdetect

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// feedArgs streams a tool call's arguments as JSON, a few bytes at a time,
// the way a provider sends them.
func feedArgs(d *Detector, content string) *Finding {
	s := argsJSON(content)
	for i := 0; i < len(s); i += 7 {
		if f := d.FeedArgs(0, s[i:min(i+7, len(s))]); f != nil {
			return f
		}
	}
	return nil
}

func argsJSON(content string) string {
	b, err := json.Marshal(map[string]string{"path": "tetris.py", "content": content})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The shape from the ladder: Qwen3-VL 8B wrote these three lines 1,045 times
// in one write call, and ran to its timeout.
func qwenVLLoop(n int) string {
	var b strings.Builder
	b.WriteString("class Tetris:\n    def spawn(self, piece_type):\n")
	for range n {
		b.WriteString("        self.current_x = 0\n        self.current_y = 0\n" +
			"        self.current_piece = self.piece_types[piece_type]\n")
	}
	return b.String()
}

func TestArgsLoopIsCaught(t *testing.T) {
	f := feedArgs(New(true), qwenVLLoop(1045))
	if f == nil {
		t.Fatal("a 1,045-copy loop in a write argument was not caught")
	}
	if f.Kind != Arguments {
		t.Errorf("kind = %q, want %q", f.Kind, Arguments)
	}
	if !strings.Contains(f.Sample, "self.current_") || strings.Contains(f.Sample, `\n`) {
		t.Errorf("sample = %q, want the repeated line, unescaped", f.Sample)
	}
	if f.Count < argMinCount {
		t.Errorf("count = %d, want at least %d", f.Count, argMinCount)
	}
}

// Caught soon after the span fills, not at the end of the call: the point is
// to stop the stream, so a check that fires only on a finished call is no
// check.
func TestArgsLoopIsCaughtEarly(t *testing.T) {
	unit := len(`        self.current_x = 0\n        self.current_y = 0\n` +
		`        self.current_piece = self.piece_types[piece_type]\n`)
	need := (argSpan+argMaxPeriod+argCheckEvery)/unit + 2
	if feedArgs(New(true), qwenVLLoop(need)) == nil {
		t.Errorf("not caught within %d copies", need)
	}
}

// Ordinary repetitive code and data stay below the bar: a long table of test
// cases, a board of identical rows, generated-looking SVG. None is a
// verbatim period over 16 KB.
func TestArgsOrdinaryCodeIsNotALoop(t *testing.T) {
	var table strings.Builder
	table.WriteString("var cases = []struct{ in, want string }{\n")
	for i := range 600 {
		fmt.Fprintf(&table, "\t{in: \"case-%03d\", want: \"result-%03d\"},\n", i, i*7)
	}
	table.WriteString("}\n")

	// 11 KB of identical rows: under the span. Twice as many would be reported.
	board := strings.Repeat("..........\n", 1000)

	var svg strings.Builder
	for y := range 20 {
		for x := range 20 {
			fmt.Fprintf(&svg, `  <rect x="%d" y="%d" width="10" height="10" fill="#cccccc"/>`+"\n", x*12, y*12)
		}
	}

	var records strings.Builder
	records.WriteString("[\n")
	for i := range 300 {
		fmt.Fprintf(&records, "  {\"id\": %d, \"enabled\": true, \"weight\": %d},\n", i, i%7)
	}
	records.WriteString("]\n")

	for name, content := range map[string]string{
		"table": table.String(), "board": board, "svg": svg.String(), "records": records.String(),
	} {
		if f := feedArgs(New(true), content); f != nil {
			t.Errorf("%s: reported as a loop: %+v", name, f)
		}
	}
}

// Parallel calls interleave on the wire. Each is judged on its own, so a
// clean call beside a looping one does not hide the loop, and the seam
// between them is not a repetition.
func TestArgsCallsAreKeptApart(t *testing.T) {
	d := New(true)
	loop := argsJSON(qwenVLLoop(1045))
	clean := argsJSON("def main():\n    pass\n")
	var found *Finding
	for i := 0; i < len(loop) && found == nil; i += 7 {
		if j := i / 7; j < len(clean) {
			if f := d.FeedArgs(1, string(clean[j])); f != nil {
				t.Fatalf("the clean call was reported: %+v", f)
			}
		}
		found = d.FeedArgs(0, loop[i:min(i+7, len(loop))])
	}
	if found == nil {
		t.Error("interleaving hid the loop")
	}
}

func TestArgsDetectionOff(t *testing.T) {
	if f := feedArgs(New(false), qwenVLLoop(1045)); f != nil {
		t.Errorf("loop_detection off still reported: %+v", f)
	}
}

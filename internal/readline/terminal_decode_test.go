package readline

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// TestConsumeANSIEscapeArrowModifiers checks how CSI arrow sequences decode,
// with emphasis on word-wise motion: Alt (modifier 3) and Ctrl (modifier 5)
// both map to Meta (word) movement, matching aider; a bare arrow moves one
// character and Shift (modifier 2) is not a word modifier.
func TestConsumeANSIEscapeArrowModifiers(t *testing.T) {
	// The sequences omit the leading ESC — consumeANSIEscape reads the byte
	// after it (here '[').
	cases := []struct {
		seq  string
		want rune
	}{
		{"[D", CharBackward},    // Left
		{"[C", CharForward},     // Right
		{"[1;3D", MetaBackward}, // Alt+Left
		{"[1;3C", MetaForward},  // Alt+Right
		{"[1;5D", MetaBackward}, // Ctrl+Left  (the fix)
		{"[1;5C", MetaForward},  // Ctrl+Right (the fix)
		{"[1;2D", CharBackward}, // Shift+Left: not word-wise
	}
	tm := &terminal{}
	for _, tc := range cases {
		var ansiBuf bytes.Buffer
		res, err := tm.consumeANSIEscape(bufio.NewReader(strings.NewReader(tc.seq)), &ansiBuf)
		if err != nil {
			t.Fatalf("seq %q: unexpected error: %v", tc.seq, err)
		}
		if res.r != tc.want {
			t.Errorf("seq %q decoded to %d, want %d", tc.seq, res.r, tc.want)
		}
	}
}

package readline

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// The word-deletion keys aider has and the fork did not decode: Alt-Backspace
// fell through to nothing, and Ctrl-Delete's modifier payload matched no case.
func TestWordDeletionKeysDecode(t *testing.T) {
	for _, tc := range []struct {
		name, seq string // the bytes after ESC
		want      rune
	}{
		{"Alt-Backspace (DEL)", "\x7f", MetaBackspace},
		{"Alt-Backspace (^H)", "\b", MetaBackspace},
		{"Alt-d", "d", MetaDelete},
		{"Ctrl-Delete", "[3;5~", MetaDelete},
		{"Delete", "[3~", MetaDeleteKey},
	} {
		var ansiBuf bytes.Buffer
		res, err := (&terminal{}).consumeANSIEscape(bufio.NewReader(strings.NewReader(tc.seq)), &ansiBuf)
		if err != nil || res.r != tc.want {
			t.Errorf("%s: decoded to %d (err %v), want %d", tc.name, res.r, err, tc.want)
		}
	}
}

// motionBuf is a buffer with no screen: Refresh applies the edit without
// drawing, which is all a motion test needs.
func motionBuf(line []rune, idx int) *runeBuffer {
	tm := &terminal{}
	tm.cfg.Store(&Config{Prompt: "> ", Painter: defaultPainter})
	return &runeBuffer{w: tm, buf: line, idx: idx}
}

// Ctrl-Right and Alt-f land on the end of a word, as aider's do, and an
// underscore is part of the word. The expected stops are the ones aider made on
// the same line through a pty.
func TestForwardWordStopsAtWordEnds(t *testing.T) {
	line := []rune("foo.bar(baz_qux) hello-world  end")
	rb := motionBuf(line, 0)
	var stops []string
	for range 6 {
		rb.ForwardWord()
		stops = append(stops, string(line[:rb.idx])+"|")
	}
	want := []string{"foo|", "foo.bar|", "foo.bar(baz_qux|", "foo.bar(baz_qux) hello|",
		"foo.bar(baz_qux) hello-world|", "foo.bar(baz_qux) hello-world  end|"}
	for i := range want {
		if stops[i] != want[i] {
			t.Errorf("stop %d = %q, want %q", i+1, stops[i], want[i])
		}
	}
	rb.ForwardWord()
	if rb.idx != len(line) {
		t.Errorf("at the end, ForwardWord moved to %d", rb.idx)
	}
}

// Ctrl-Left is unchanged except that snake_case is one word: from the end of
// "baz_qux" it goes to "baz", not to "qux".
func TestBackwardWordKeepsSnakeCaseWhole(t *testing.T) {
	line := []rune("call(baz_qux)")
	rb := motionBuf(line, len("call(baz_qux"))
	rb.MoveToPrevWord()
	if got := string(line[rb.idx:]); got != "baz_qux)" {
		t.Errorf("Ctrl-Left landed before %q, want before \"baz_qux)\"", got)
	}
}

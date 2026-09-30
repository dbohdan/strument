package readline

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/readline/internal/runes"
)

// A bracketed paste decodes as one block: its newlines, however the terminal
// spells them, become \n, and anything else that is a control character —
// an ESC above all — is text that must not be read as a key.
func TestBracketedPasteDecodesAsOneBlock(t *testing.T) {
	seq := "[200~first\r\nsecond\rthird\nfourth\tend\x1b[Dx\x1b[201~"
	var ansiBuf bytes.Buffer
	res, err := (&terminal{}).consumeANSIEscape(bufio.NewReader(strings.NewReader(seq)), &ansiBuf)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(res.paste), "first\nsecond\nthird\nfourth\tend[Dx"; got != want {
		t.Errorf("paste = %q, want %q", got, want)
	}
}

// Before the fix, the first newline of a multi-line paste submitted the line.
// Now the whole paste is one MetaPaste event: what follows it in the stream is
// read as the next key, not as part of the paste.
func TestPasteIsOneKeyAndReadingResumesAfterIt(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("[200~a\rb\x1b[201~z"))
	var ansiBuf bytes.Buffer
	res, err := (&terminal{}).consumeANSIEscape(r, &ansiBuf)
	if err != nil || string(res.paste) != "a\nb" {
		t.Fatalf("paste = %q, err %v", string(res.paste), err)
	}
	if next, _, _ := r.ReadRune(); next != 'z' {
		t.Errorf("the rune after the paste is %q, want 'z'", next)
	}
}

// In the buffer a newline is one glyph cell, not a row break; in the prompt
// it is still a row break. Treating the buffer's as a break made the redraw
// walk up rows the screen never had and erase the input.
func TestSplitByLineBreaksOnlyAtPromptNewlines(t *testing.T) {
	rows := runes.SplitByLine([]rune("> "), []rune("ab\ncd"), 0, 80, 1)
	if len(rows) != 1 {
		t.Errorf("a buffer newline made %d rows, want 1: %q", len(rows), rows)
	}
	rows = runes.SplitByLine([]rune("line one\n> "), []rune("ab"), 0, 80, 1)
	if len(rows) != 2 {
		t.Errorf("a prompt newline made %d rows, want 2: %q", len(rows), rows)
	}
	if w := runes.WidthAll([]rune("ab\ncd")); w != 5 {
		t.Errorf("width of a buffer with a newline = %d, want 5 (the glyph is one cell)", w)
	}
}

// The newline is drawn as the glyph whatever painter is installed — the REPL
// installs its own, which is how the first version missed it.
func TestNewlineDrawsAsGlyphUnderACustomPainter(t *testing.T) {
	rb := newRedrawTestBuf(80, "> ", []rune("ab\ncd"), 5)
	rb.getConfig().Painter = func(line []rune, _ int) []rune {
		return append(append([]rune("\x1b[32m"), line...), []rune("\x1b[0m")...)
	}
	var b bytes.Buffer
	rb.writeBuffer(&b)
	if got := b.String(); !strings.Contains(got, "ab↵cd") || strings.Contains(got, "\n") {
		t.Errorf("drew %q, want the newline as ↵ and no raw newline", got)
	}
}

// History entries with line breaks stay one line on disk and come back whole;
// an entry without them is stored exactly as before.
func TestHistoryLineEncoding(t *testing.T) {
	for _, s := range []string{"plain line", `printf("\n")`, "one\ntwo\nthree"} {
		enc := encodeHistoryLine(s)
		if strings.Contains(enc, "\n") {
			t.Errorf("encoded %q still has a newline: %q", s, enc)
		}
		if got := decodeHistoryLine(enc); got != s {
			t.Errorf("round trip of %q gave %q", s, got)
		}
	}
	if encodeHistoryLine(`printf("\n")`) != `printf("\n")` {
		t.Error("an entry with a typed backslash-n changed on disk")
	}
}

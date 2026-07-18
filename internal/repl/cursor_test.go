package repl

import (
	"bytes"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/render"
)

func TestStreamHidesCursorOnceAndRestores(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: true, theme: render.DefaultTheme()}

	o.StreamText("hello")
	o.StreamText(" world")
	o.FlushStream()
	got := buf.String()

	const hide, show = "\x1b[?25l", "\x1b[?25h"
	if n := strings.Count(got, hide); n != 1 {
		t.Errorf("want exactly one hide-cursor, got %d:\n%q", n, got)
	}
	if n := strings.Count(got, show); n != 1 {
		t.Errorf("want exactly one show-cursor, got %d:\n%q", n, got)
	}
	if hi, sh := strings.Index(got, hide), strings.LastIndex(got, show); hi < 0 || hi >= sh {
		t.Errorf("hide must precede show: hide=%d show=%d\n%q", hi, sh, got)
	}
	// A second flush without new streaming must not re-emit the show.
	buf.Reset()
	o.FlushStream()
	if strings.Contains(buf.String(), show) {
		t.Errorf("showCursor should be idempotent, got %q", buf.String())
	}
}

func TestStreamNoCursorEscapesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	o := &termOutput{w: &buf, color: false, theme: render.DefaultTheme()}
	o.StreamReasoning("thinking")
	o.StreamText("answer")
	o.FlushStream()
	if strings.Contains(buf.String(), "\x1b[?25") {
		t.Errorf("no-color must not emit cursor escapes:\n%q", buf.String())
	}
}

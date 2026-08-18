package render

import (
	"bytes"
	"strings"
	"testing"
)

// collect runs an ArgScanner over the fragments and returns the decoded value
// accumulated per field.
func collect(fragments ...string) map[string]string {
	got := map[string]*strings.Builder{}
	s := NewArgScanner(func(field, chunk string) {
		b := got[field]
		if b == nil {
			b = &strings.Builder{}
			got[field] = b
		}
		b.WriteString(chunk)
	})
	for _, f := range fragments {
		s.Write(f)
	}
	out := map[string]string{}
	for k, v := range got {
		out[k] = v.String()
	}
	return out
}

// splitBytes returns single-byte fragments, forcing the scanner to survive
// mid-escape and mid-UTF-8 boundaries.
func splitBytes(s string) []string {
	frags := make([]string, len(s))
	for i := range len(s) {
		frags[i] = s[i : i+1]
	}
	return frags
}

func TestArgScannerDecodes(t *testing.T) {
	// search uses \n and \t escapes; replace carries a raw multibyte rune (em
	// dash) and a \u-escaped one, exercising both UTF-8 paths.
	args := `{"path":"main.go","old_string":"foo\n\tbar","new_string":"foo — baz—done"}`
	want := map[string]string{
		"path":       "main.go",
		"old_string": "foo\n\tbar",
		"new_string": "foo — baz—done",
	}

	t.Run("one blob", func(t *testing.T) {
		assertFields(t, collect(args), want)
	})
	t.Run("byte by byte", func(t *testing.T) {
		assertFields(t, collect(splitBytes(args)...), want)
	})
}

func assertFields(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fields: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %q: got %q, want %q", k, got[k], v)
		}
	}
}

// TestArgScannerSkipsNested confirms values that are not top-level strings
// (nested objects, arrays) are ignored, so only the flat code-bearing fields
// stream.
func TestArgScannerSkipsNested(t *testing.T) {
	args := `{"paths":["a.go","b.go"],"reason":"need context","meta":{"k":"v"}}`
	want := map[string]string{"reason": "need context"}
	assertFields(t, collect(args), want)
	assertFields(t, collect(splitBytes(args)...), want)
}

func renderDiff(t *testing.T, tool string, color bool, fragments []string) string {
	t.Helper()
	var sb strings.Builder
	d := NewToolDiff(&sb, color, DefaultTheme(), tool)
	for _, f := range fragments {
		d.Write(f)
	}
	d.Flush()
	return sb.String()
}

func TestToolDiffRedGreen(t *testing.T) {
	args := `{"path":"main.go","old_string":"old one\nold two","new_string":"new one\nnew two"}`
	want := "main.go\n- old one\n- old two\n+ new one\n+ new two\n"

	t.Run("one blob", func(t *testing.T) {
		if got := renderDiff(t, "edit", false, []string{args}); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
	t.Run("byte by byte", func(t *testing.T) {
		if got := renderDiff(t, "edit", false, splitBytes(args)); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
}

func TestToolDiffCreateFile(t *testing.T) {
	args := `{"path":"new.go","content":"package main\n\nfunc main() {}"}`
	want := "new.go (whole file)\n+ package main\n+ \n+ func main() {}\n"
	if got := renderDiff(t, "write", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if got := renderDiff(t, "write", false, splitBytes(args)); got != want {
		t.Errorf("split got:\n%q\nwant:\n%q", got, want)
	}
}

func TestToolDiffColor(t *testing.T) {
	args := `{"path":"a.go","old_string":"x","new_string":"y"}`
	got := renderDiff(t, "edit", true, []string{args})
	want := "a.go\n\x1b[31m- x\x1b[0m\n\x1b[32m+ y\x1b[0m\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffTrailingNoNewline confirms a value with no trailing newline
// still flushes its last line on Flush.
func TestToolDiffTrailingNoNewline(t *testing.T) {
	args := `{"path":"a.go","old_string":"only line"}`
	want := "a.go\n- only line\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffPathLast reproduces a provider (Qwen3.6) that streams search
// and replace before path: the header must still print first, above the diff
// lines, not spliced into the middle.
func TestToolDiffPathLast(t *testing.T) {
	args := `{"old_string":"old line","new_string":"new line","path":"a.go"}`
	want := "a.go\n- old line\n+ new line\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("one blob:\ngot:\n%q\nwant:\n%q", got, want)
	}
	if got := renderDiff(t, "edit", false, splitBytes(args)); got != want {
		t.Errorf("byte by byte:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffPathMiddle covers path arriving between search and replace, the
// exact order seen live against Qwen3.6.
func TestToolDiffPathMiddle(t *testing.T) {
	args := `{"old_string":"a\nb","path":"a.go","new_string":"c\nd"}`
	want := "a.go\n- a\n- b\n+ c\n+ d\n"
	if got := renderDiff(t, "edit", false, splitBytes(args)); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffReplaceBeforeSearch reproduces a provider (GLM-5.2) that streams
// the replace field before search: the removed (-) lines must still print
// above the added (+) lines, in canonical git-diff order.
func TestToolDiffReplaceBeforeSearch(t *testing.T) {
	args := `{"path":"a.go","new_string":"new one\nnew two","old_string":"old one\nold two"}`
	want := "a.go\n- old one\n- old two\n+ new one\n+ new two\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("one blob:\ngot:\n%q\nwant:\n%q", got, want)
	}
	if got := renderDiff(t, "edit", false, splitBytes(args)); got != want {
		t.Errorf("byte by byte:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffReplaceBeforeSearchPathLast combines both reversals seen live:
// replace and search stream before path, and replace before search.
func TestToolDiffReplaceBeforeSearchPathLast(t *testing.T) {
	args := `{"new_string":"new","old_string":"old","path":"a.go"}`
	want := "a.go\n- old\n+ new\n"
	if got := renderDiff(t, "edit", false, splitBytes(args)); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffContext is the case the diff exists for: the edit tool asks for
// surrounding lines so the search matches uniquely, and those lines must read
// as context rather than as a wholesale removal and re-addition.
func TestToolDiffContext(t *testing.T) {
	args := `{"path":"a.go","old_string":"func foo() {\n  x := 1\n  return x\n}",` +
		`"new_string":"func foo() {\n  x := 2\n  return x\n}"}`
	want := "a.go\n" +
		"  func foo() {\n" +
		"-   x := 1\n" +
		"+   x := 2\n" +
		"    return x\n" +
		"  }\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("one blob:\ngot:\n%q\nwant:\n%q", got, want)
	}
	// Buffering must be indifferent to where the provider splits the stream.
	if got := renderDiff(t, "edit", false, splitBytes(args)); got != want {
		t.Errorf("byte by byte:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffContextColor confirms unchanged lines carry no color, so the
// changed ones are what the eye lands on.
func TestToolDiffContextColor(t *testing.T) {
	args := `{"path":"a.go","old_string":"keep\nold","new_string":"keep\nnew"}`
	want := "a.go\n  keep\n\x1b[31m- old\x1b[0m\n\x1b[32m+ new\x1b[0m\n"
	if got := renderDiff(t, "edit", true, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffElidesLongContext keeps a generous old_string from burying the
// line that changed: only diffContext lines survive on each side of it, and
// what was left out is counted rather than dropped silently.
func TestToolDiffElidesLongContext(t *testing.T) {
	args := `{"path":"a.go",` +
		`"old_string":"a\nb\nc\nd\ne\nf\ng\nh\nX\ni\nj\nk\nl\nm\nn\no",` +
		`"new_string":"a\nb\nc\nd\ne\nf\ng\nh\nY\ni\nj\nk\nl\nm\nn\no"}`
	want := "a.go\n" +
		"  … 5 unchanged lines …\n" +
		"  f\n  g\n  h\n" +
		"- X\n+ Y\n" +
		"  i\n  j\n  k\n" +
		"  … 4 unchanged lines …\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffKeepsOneHiddenLine: a marker that hides a single line costs the
// line it saves, so the line wins. Caught by watching a real edit render
// "… 1 unchanged lines …" — which is also the grammar this rules out.
func TestToolDiffKeepsOneHiddenLine(t *testing.T) {
	args := `{"path":"a.go","old_string":"X\na\nb\nc\nd","new_string":"Y\na\nb\nc\nd"}`
	want := "a.go\n- X\n+ Y\n  a\n  b\n  c\n  d\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffPureInsertAndDelete covers the degenerate shapes: an empty side
// falls out of the opcodes rather than needing a case of its own.
func TestToolDiffPureInsertAndDelete(t *testing.T) {
	insert := `{"path":"a.go","old_string":"","new_string":"one\ntwo"}`
	if got, want := renderDiff(t, "edit", false, []string{insert}), "a.go\n+ one\n+ two\n"; got != want {
		t.Errorf("insert got:\n%q\nwant:\n%q", got, want)
	}
	del := `{"path":"a.go","old_string":"one\ntwo","new_string":""}`
	if got, want := renderDiff(t, "edit", false, []string{del}), "a.go\n- one\n- two\n"; got != want {
		t.Errorf("delete got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffNoChange: an edit whose sides are identical elides nothing —
// there is no change for the context to sit beside, so showing it all is the
// honest rendering of a no-op.
func TestToolDiffNoChange(t *testing.T) {
	args := `{"path":"a.go","old_string":"a\nb\nc\nd\ne\nf\ng\nh","new_string":"a\nb\nc\nd\ne\nf\ng\nh"}`
	want := "a.go\n  a\n  b\n  c\n  d\n  e\n  f\n  g\n  h\n"
	if got := renderDiff(t, "edit", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffCommand renders a command field: no header (no path), the command
// on a "$" line, and the purpose ignored. ToolDiff keeps this ability even
// though RendersDiff no longer routes bash to it — the "$ " line the
// confirmation prompt draws is this same shape.
func TestToolDiffCommand(t *testing.T) {
	args := `{"command":"go test ./...","purpose":"run the tests"}`
	want := "$ go test ./...\n"
	if got := renderDiff(t, "bash", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if got := renderDiff(t, "bash", false, splitBytes(args)); got != want {
		t.Errorf("split got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffSet fans two interleaved tool calls out to separate diffs and
// flushes them in call order.
func TestToolDiffSet(t *testing.T) {
	var sb strings.Builder
	s := NewToolDiffSet(&sb, false, DefaultTheme())
	// Two calls arriving interleaved by index, name only on the first frag.
	s.Write(0, "edit", `{"path":"a.go",`)
	s.Write(1, "write", `{"path":"b.go",`)
	s.Write(0, "", `"old_string":"x","new_string":"y"}`)
	s.Write(1, "", `"content":"new"}`)
	s.Flush()
	want := "a.go\n- x\n+ y\nb.go (whole file)\n+ new\n"
	if sb.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", sb.String(), want)
	}
}

// TestToolDiffSetNoInterleave reproduces a provider streaming a second tool
// call's argument in the middle of the first's (seen live against DeepSeek):
// the write diff must stay contiguous, with the second call's rendering after
// it rather than spliced between its lines.
func TestToolDiffSetNoInterleave(t *testing.T) {
	var sb strings.Builder
	s := NewToolDiffSet(&sb, false, DefaultTheme())
	s.Write(0, "write", `{"path":"hello.sh","content":"#!/bin/bash\n`)
	s.Write(1, "edit", `{"path":"run.sh",`)
	s.Write(0, "", `echo Hi"}`)
	s.Write(1, "", `"old_string":"x","new_string":"y"}`)
	s.Flush()
	want := "hello.sh (whole file)\n+ #!/bin/bash\n+ echo Hi\nrun.sh\n- x\n+ y\n"
	if sb.String() != want {
		t.Errorf("interleaved streams garbled:\ngot:\n%q\nwant:\n%q", sb.String(), want)
	}
}

// TestToolDiffSetSkipsBash: a bash command is not streamed. A live run showed
// it reaching the terminal three times for one call — here, again in the
// confirmation prompt, and again as "Running …" at execution — and this is the
// copy worth losing, since watching a one-line command arrive character by
// character is worth little and the other two sit where a reader needs them.
func TestToolDiffSetSkipsBash(t *testing.T) {
	var sb strings.Builder
	s := NewToolDiffSet(&sb, false, DefaultTheme())
	s.Write(0, "bash", `{"command":"go test ./...","purpose":"run the tests"}`)
	s.Flush()
	if sb.String() != "" {
		t.Errorf("bash streamed %q; the prompt and the run echo it already", sb.String())
	}
}

// TestToolDiffSetSkipsObservationTools pins the fix for a real display bug: the
// observation tools also take a "path" argument, and a diff renderer that keys
// on that alone printed a bare, unlabeled path line for every read and ls, with
// no diff beneath it. Their outcome is reported when they run, not while their
// arguments stream.
func TestToolDiffSetSkipsObservationTools(t *testing.T) {
	for _, tool := range []string{"read", "ls", "grep", "glob", "check"} {
		var buf bytes.Buffer
		s := NewToolDiffSet(&buf, false, DefaultTheme())
		s.Write(0, tool, `{"path":"internal/parse/parse.go","pattern":"x"}`)
		s.Flush()
		if buf.Len() != 0 {
			t.Errorf("%s rendered %q, want nothing", tool, buf.String())
		}
	}
}

// TestToolDiffSetStillRendersEdits guards the other half: skipping observation
// tools must not skip a real edit that follows one in the same turn.
func TestToolDiffSetStillRendersEdits(t *testing.T) {
	var buf bytes.Buffer
	s := NewToolDiffSet(&buf, false, DefaultTheme())
	s.Write(0, "read", `{"path":"a.go"}`)
	s.Write(1, "edit", `{"path":"a.go","old_string":"x\n","new_string":"y\n"}`)
	s.Flush()

	want := "a.go\n- x\n+ y\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

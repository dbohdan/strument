package render

import (
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
	args := `{"path":"main.go","search":"foo\n\tbar","replace":"foo — baz—done"}`
	want := map[string]string{
		"path":    "main.go",
		"search":  "foo\n\tbar",
		"replace": "foo — baz—done",
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
	d := NewToolDiff(&sb, color, tool)
	for _, f := range fragments {
		d.Write(f)
	}
	d.Flush()
	return sb.String()
}

func TestToolDiffRedGreen(t *testing.T) {
	args := `{"path":"main.go","search":"old one\nold two","replace":"new one\nnew two"}`
	want := "main.go\n- old one\n- old two\n+ new one\n+ new two\n"

	t.Run("one blob", func(t *testing.T) {
		if got := renderDiff(t, "replace_in_file", false, []string{args}); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
	t.Run("byte by byte", func(t *testing.T) {
		if got := renderDiff(t, "replace_in_file", false, splitBytes(args)); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
}

func TestToolDiffCreateFile(t *testing.T) {
	args := `{"path":"new.go","content":"package main\n\nfunc main() {}"}`
	want := "new.go (new file)\n+ package main\n+ \n+ func main() {}\n"
	if got := renderDiff(t, "create_file", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if got := renderDiff(t, "create_file", false, splitBytes(args)); got != want {
		t.Errorf("split got:\n%q\nwant:\n%q", got, want)
	}
}

func TestToolDiffColor(t *testing.T) {
	args := `{"path":"a.go","search":"x","replace":"y"}`
	got := renderDiff(t, "replace_in_file", true, []string{args})
	want := "a.go\n\x1b[31m- x\x1b[0m\n\x1b[32m+ y\x1b[0m\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffTrailingNoNewline confirms a value with no trailing newline
// still flushes its last line on Flush.
func TestToolDiffTrailingNoNewline(t *testing.T) {
	args := `{"path":"a.go","search":"only line"}`
	want := "a.go\n- only line\n"
	if got := renderDiff(t, "replace_in_file", false, []string{args}); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestToolDiffSet fans two interleaved tool calls out to separate diffs and
// flushes them in call order.
func TestToolDiffSet(t *testing.T) {
	var sb strings.Builder
	s := NewToolDiffSet(&sb, false)
	// Two calls arriving interleaved by index, name only on the first frag.
	s.Write(0, "replace_in_file", `{"path":"a.go",`)
	s.Write(1, "create_file", `{"path":"b.go",`)
	s.Write(0, "", `"search":"x","replace":"y"}`)
	s.Write(1, "", `"content":"new"}`)
	s.Flush()
	want := "a.go\n- x\n+ y\nb.go (new file)\n+ new\n"
	if sb.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", sb.String(), want)
	}
}

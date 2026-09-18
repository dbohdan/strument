package coder

import (
	"context"
	"strings"
	"testing"
)

// The arms have to differ in exactly the intended way, and the handbook's
// pre-run checklist asks for that to be demonstrated rather than assumed —
// "compare the artifacts, and refuse identical arms". Two arms that produce the
// same description produce a clean null for a reason that has nothing to do
// with the question.
func TestCodeErgoArmsDiffer(t *testing.T) {
	tools := InspectorTools()
	off := codeTool(tools, CodeResultLast, CodeNSFlat, false).Description
	on := codeTool(tools, CodeResultLast, CodeNSFlat, true).Description

	if off == on {
		t.Error("the signatures arm produces a description identical to the baseline")
	}
	if strings.Contains(off, "by position") {
		t.Error("the baseline already documents a positional order")
	}
	for _, want := range []string{"read(path, offset, limit)", "grep(pattern, path, glob"} {
		if !strings.Contains(on, want) {
			t.Errorf("the signatures arm does not state %q:\n%s", want, on)
		}
	}
	// The signature line is rendered from codeToolParams, so it cannot promise
	// an order the bridge does not bind. Asserted because a hand-written line
	// would be the obvious way to write this and would drift the first time the
	// table changed.
	if !strings.Contains(on, "read_text(path, offset, limit)") {
		t.Errorf("the signature line omits the code functions:\n%s", on)
	}
	// read_text is shipped, so every arm names it.
	if !strings.Contains(off, "read_text(path, offset=0, limit=0)") {
		t.Errorf("read_text is not described:\n%s", off)
	}
}

// read_text is registered and returns the file as stored, not as read formats it.
func TestReadTextIsRegistered(t *testing.T) {
	const body = "alpha\nbeta\ngamma\n"
	c, _ := observeEnv(t, map[string]string{"f.txt": body})

	got := c.runCode(context.Background(), codeCall{code: `print(len(read_text("f.txt")))`})
	if want := "17"; !strings.Contains(got, want) {
		t.Errorf("read_text did not return the file as stored (want length %s):\n%s", want, got)
	}
	// The point of the function: read's own result carries the header and the
	// line-number prefixes, so measuring it measures the harness.
	viaRead := c.runCode(context.Background(), codeCall{code: `print(len(read("f.txt")))`})
	if viaRead == got {
		t.Error("read and read_text returned the same length; the arm changes nothing")
	}
}

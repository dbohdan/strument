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
	base := codeTool(tools, CodeResultLast, CodeNSFlat, false, false).Description
	sigs := codeTool(tools, CodeResultLast, CodeNSFlat, true, false).Description
	text := codeTool(tools, CodeResultLast, CodeNSFlat, false, true).Description
	both := codeTool(tools, CodeResultLast, CodeNSFlat, true, true).Description

	for _, tc := range []struct{ name, a, b string }{
		{"signatures", base, sigs},
		{"read_text", base, text},
		{"both against each", sigs, both},
		{"both against signatures alone", text, both},
	} {
		if tc.a == tc.b {
			t.Errorf("the %s arm produces a description identical to the one it is compared against", tc.name)
		}
	}

	// And they differ in the intended way, not merely somehow.
	if strings.Contains(base, "read_text") || strings.Contains(sigs, "read_text") {
		t.Error("read_text is named in an arm that does not offer it, so a program would call what is not registered")
	}
	if !strings.Contains(text, "read_text(path, offset=0, limit=0)") {
		t.Errorf("the read_text arm does not state the function's signature:\n%s", text)
	}
	if strings.Contains(base, "by position") {
		t.Error("the baseline already documents a positional order")
	}
	for _, want := range []string{"read(path, offset, limit)", "grep(pattern, path, glob"} {
		if !strings.Contains(sigs, want) {
			t.Errorf("the signatures arm does not state %q:\n%s", want, sigs)
		}
	}
	// The signature line is rendered from codeToolParams, so it cannot promise
	// an order the bridge does not bind. Asserted because a hand-written line
	// would be the obvious way to write this and would drift the first time the
	// table changed.
	if !strings.Contains(both, "read_text(path, offset, limit)") {
		t.Errorf("the signature line omits the code functions:\n%s", both)
	}
}

// read_text has to exist when the arm says it does, and not otherwise — the
// checklist's "did the mechanism fire", asserted where it is cheap.
func TestReadTextArmRegistersTheFunction(t *testing.T) {
	const body = "alpha\nbeta\ngamma\n"
	c, _ := observeEnv(t, map[string]string{"f.txt": body})

	c.CodeReadText = true
	got := c.runCode(context.Background(), codeCall{code: `print(len(read_text("f.txt")))`})
	if want := "16"; !strings.Contains(got, want) {
		t.Errorf("read_text did not return the file as stored (want length %s):\n%s", want, got)
	}
	// The point of the function: read's own result carries the header and the
	// line-number prefixes, so measuring it measures the harness.
	viaRead := c.runCode(context.Background(), codeCall{code: `print(len(read("f.txt")))`})
	if viaRead == got {
		t.Error("read and read_text returned the same length; the arm changes nothing")
	}

	c.CodeReadText = false
	if got := c.runCode(context.Background(), codeCall{code: `print(read_text("f.txt"))`}); !strings.Contains(got, "read_text") {
		t.Errorf("with the arm off, read_text should raise a NameError naming it:\n%s", got)
	}
}

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/workspace"
)

// The code functions' tests — the run_code-only callable functions of
// codefuncs.go. The mechanism's contract is pinned at the level the program
// sees: data returned, errors raised, and the run_code-only constraint itself.

// codeEnv is observeEnv plus a binary file: observeEnv's map-of-strings
// cannot express bytes that are not valid UTF-8 text.
func codeEnv(t *testing.T, rel string, data []byte) *Coder {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out := &captureOut{}
	return &Coder{Root: root, Out: out, Files: workspace.New(root)}
}

// TestCodeFuncMagicNumber is the use case the function was built for: identify
// a file by its magic bytes and compute over the payload, all in one program.
func TestCodeFuncMagicNumber(t *testing.T) {
	// A minimal ELF-shaped file: magic, class, data, version, then filler.
	data := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, make([]byte, 16)...)
	c := codeEnv(t, "probe.bin", data)

	got := c.runCode(context.Background(), codeCall{code: `
d = read_bin(path="probe.bin")
is_elf = d["data"][:4] == [127, 69, 76, 70]
print("elf:", is_elf)
print("size:", d["size"])
print("truncated:", d["truncated"])
`})
	for _, want := range []string{"elf: True", "size: 24", "truncated: False"} {
		if !strings.Contains(got, want) {
			t.Errorf("read_bin result missing %q:\n%s", want, got)
		}
	}
}

// TestCodeFuncPagesAndCaps covers the window arithmetic: offset/limit page,
// truncation is reported, an offset past EOF yields an empty window that is
// not a failure, and an over-large limit is clamped rather than granted.
func TestCodeFuncPagesAndCaps(t *testing.T) {
	data := make([]byte, 10)
	for i := range data {
		data[i] = byte(i)
	}
	c := codeEnv(t, "small.bin", data)

	got := c.runCode(context.Background(), codeCall{code: `
w1 = read_bin(path="small.bin", offset=6, limit=4)
w2 = read_bin(path="small.bin", offset=6, limit=100000)
w3 = read_bin(path="small.bin", offset=500)
print(w1["data"], w1["truncated"])
print(w2["data"], w2["truncated"])
print(w3["data"], w3["truncated"], w3["size"])
`})
	for _, want := range []string{
		"[6, 7, 8, 9] False", // ends exactly at EOF: complete, not truncated
		"[6, 7, 8, 9] False", // clamped, and the clamp is not truncation of content
		"[] False 10",        // past EOF: empty, not an error
	} {
		if !strings.Contains(got, want) {
			t.Errorf("paging result missing %q:\n%s", want, got)
		}
	}
}

// TestCodeFuncErrorsAreExceptions pins the error channel: a missing path and
// an out-of-root path raise, with the tool's own sentence as the message —
// not a string return the program might treat as data.
func TestCodeFuncErrorsAreExceptions(t *testing.T) {
	c, _ := observeEnv(t, nil)

	got := c.runCode(context.Background(), codeCall{code: `read_bin()`})
	if !strings.Contains(got, "read_bin requires a \"path\" argument") {
		t.Errorf("a missing path must raise, got:\n%s", got)
	}

	got = c.runCode(context.Background(), codeCall{code: `read_bin(path="../outside.bin")`})
	if !strings.Contains(got, "Could not read") {
		t.Errorf("an escaping path must raise the read failure, got:\n%s", got)
	}
}

// TestCodeFuncNotADirectTool pins the run_code-only constraint: the name is
// invisible to Inspector.Run, so a direct tool call reads "Unknown tool".
// This is the test that stays red if someone "fixes" the function into the
// observation dispatch.
func TestCodeFuncNotADirectTool(t *testing.T) {
	c := codeEnv(t, "x.bin", []byte{1})

	if got := c.inspector().Run("read_bin", `{"path":"x"}`); !strings.Contains(got, "Unknown tool") {
		t.Errorf("read_bin must not be a direct tool, got: %q", got)
	}
	if got := c.runObservationRedirect(call("read_bin", `{"path":"x"}`)); !strings.Contains(got, "Unknown tool") {
		t.Errorf("the redirect path must not recognize read_bin either, got: %q", got)
	}
}

// TestCodeFuncDocMatchesRegistry pins the description/registry drift guard:
// every registered function's name and signature must appear in the tool
// description the model actually sees.
func TestCodeFuncDocMatchesRegistry(t *testing.T) {
	desc := codeTool().Description
	for _, d := range codeFuncs {
		name := d.name + "("
		if !strings.Contains(desc, name) {
			t.Errorf("the run_code description does not name %q; the model cannot call what it is not told about", name)
		}
	}
	if !strings.Contains(desc, "0-255") {
		t.Errorf("the description must state read_bin's return shape; got:\n%s", desc)
	}
}

// TestCodeErrorAttribution pins the fix for the failure mode observed in the
// 2026-09 tool trial: a tool-call failure inside a multi-call program used to
// come back as a flat one-liner with no line attribution, because the Go side
// dropped the snapshot instead of resuming it with the error. The error must
// be raised at the call site, so the traceback names the program line that
// made the failing call — the thing a model debugging a 20-line program
// needs.
func TestCodeErrorAttribution(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"x.txt": "hi\n"})

	// Two successful calls, then the failure: the traceback must name the
	// failing line (5), not the first call or nothing at all.
	code := `
files = glob(pattern="*.go")
a = read(path="x.txt")
b = read(path="no-such-file")
b`
	got := c.runCode(context.Background(), codeCall{code: code})
	if !strings.Contains(got, "line 4") || !strings.Contains(got, `read(path="no-such-file")`) {
		t.Errorf("a tool failure must be attributed to its call site, got:\n%s", got)
	}
	if !strings.Contains(got, "Could not read") {
		t.Errorf("the tool's own error sentence must survive, got:\n%s", got)
	}
}

// TestCodeErrorIsCatchable pins that a tool failure is an ordinary Python
// exception: a program may catch it and continue. This is what makes the
// attribution upgrade semantically safe — it did not just relabel errors, it
// made them catchable — and it is why the bridge-call cap is pinned in its
// uncaught form in TestCodeBridgeCapFires: a caught cap no longer stops the
// program, the duration limit does.
func TestCodeErrorIsCatchable(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"x.txt": "hi\n"})

	code := "r = 'fallback'\ntry:\n    read(path='missing')\nexcept Exception as e:\n    r = 'caught'\nr"
	if got := c.runCode(context.Background(), codeCall{code: code}); !strings.Contains(got, "caught") {
		t.Errorf("a tool failure must be catchable as an ordinary exception, got:\n%s", got)
	}
}

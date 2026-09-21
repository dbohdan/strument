package main

// The tool command's CLI-level tests, run against the built binary: the door's
// contract is a shell pipeline's view of it, which a test inside internal/coder
// cannot see — that stdout holds only the result, the announcement went to
// stderr, and the mapping from command name to tool name is exact.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolRunCodeStdoutIsOnlyTheResult pins the door's output discipline:
// stdout is what the model would receive, byte for byte; stderr carries the
// program block and the outcome line. A result with no trailing newline stays
// without one through a pipe — `| wc -c` measures the result, not a
// cosmetic newline this command added.
func TestToolRunCodeStdoutIsOnlyTheResult(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(builtBinary, args...)
		cmd.Dir = root
		var o, e strings.Builder
		cmd.Stdout, cmd.Stderr = &o, &e
		if err := cmd.Run(); err != nil {
			t.Fatalf("strument %s: %v\nstderr: %s", strings.Join(args, " "), err, e.String())
		}
		return o.String(), e.String()
	}

	// A print-driven program: its output is what the model would see. The
	// result carries no trailing newline through a pipe — what the model would
	// be sent is not repainted — while a terminal gets the papercut newline.
	out, errOut := run("tool", "run_code", `print("hello")
print("world")`)
	if out != "hello\nworld" {
		t.Errorf("stdout = %q, want the program's printed output; the result alone is what a pipe measures", out)
	}
	if !strings.Contains(errOut, "‹run_code›") || !strings.Contains(errOut, "Ran 2 lines of code.") {
		t.Errorf("stderr must carry the program block and the outcome line, got:\n%s", errOut)
	}
	if strings.Contains(out, "Ran") {
		t.Errorf("the outcome line must not mix into stdout, got:\n%s", out)
	}
}

// TestToolRunCodeDataShapes is the incident that motivated the command, end to
// end from the shell: a glob result is a list to sort, not prose to iterate.
func TestToolRunCodeDataShapes(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(builtBinary, "tool", "run_code", `sorted(glob("*.go"))`)
	cmd.Dir = root
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("tool run_code: %v\nstderr: %s", err, errOut.String())
	}
	if strings.TrimSpace(out.String()) != `["a.go","b.go"]` {
		t.Errorf("glob must return sorted data, got:\n%s", out.String())
	}
}

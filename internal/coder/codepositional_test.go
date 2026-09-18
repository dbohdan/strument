package coder

import (
	"context"
	"strings"
	"testing"
)

// The reported failure, reproduced: DeepSeek V4.1 Flash wrote
// `read("README.md")`, got nothing back, and spent a second step writing
// `read(path="README.md")`.
//
// It is worth a behavioural test rather than a table one because of how it
// failed. Monty does not refuse a positional argument it has no name for — it
// discards it, so the program runs, the tool is called with {}, and what comes
// back is a complaint about a missing path for a call that plainly supplied
// one. Nothing in the harness said "positional arguments do not work", which is
// the property this pins.
func TestPositionalCallsReachTheTool(t *testing.T) {
	const body = "alpha\nbeta\ngamma\ndelta\n"
	c, _ := observeEnv(t, map[string]string{"README.md": body, "src/a.go": "package a\n"})

	for _, tc := range []struct {
		name, code string
		want       []string
		absent     string
	}{
		{
			name: "read, the reported case",
			code: `read("README.md")`,
			want: []string{"alpha", "gamma"},
		},
		{
			name: "read with a second positional binds offset, not limit",
			code: `read("README.md", 3)`,
			want: []string{"gamma"},
			// Binding 3 to limit instead would return the first three lines,
			// which is the silent wrong answer an alphabetical order gives.
			absent: "alpha",
		},
		{
			name: "grep's second positional is the path, as the shell spells it",
			code: `grep("package", "src")`,
			want: []string{"src/a.go"},
		},
		{
			name: "glob",
			code: `glob("**/*.go")`,
			want: []string{"src/a.go"},
		},
		{
			name: "ls",
			code: `ls("src")`,
			want: []string{"a.go"},
		},
		{
			name: "keywords still work, and mix with positionals",
			code: `read("README.md", limit=1)`,
			want: []string{"alpha"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: "print(" + tc.code + ")"})
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("%s did not reach the tool — result lacks %q:\n%s", tc.code, want, got)
				}
			}
			if tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Errorf("%s bound its second argument to the wrong parameter; %q should not appear:\n%s",
					tc.code, tc.absent, got)
			}
		})
	}
}

// An argument the tool does not know still crosses as the model wrote it, so
// the tool answers it rather than the bridge swallowing it. Registering the
// parameter names must not turn the bridge into a second validator with its own
// idea of what each tool accepts.
func TestUnknownKeywordsStillReachTheTool(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"README.md": "alpha\n"})
	got := c.runCode(context.Background(), codeCall{code: `print(read(path="README.md", nope=1))`})
	if !strings.Contains(got, "alpha") {
		t.Errorf("an unknown keyword stopped a valid call from working:\n%s", got)
	}
}

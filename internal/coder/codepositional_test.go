package coder

import (
	"strings"
	"testing"
)

// The reported failure, reproduced: DeepSeek V4.1 Flash wrote
// `read("README.md")`, got nothing back, and spent a second step writing
// `read(path="README.md")`.
//
// It is worth a behavioural test rather than a table one because of how it
// failed. The interpreter (Monty, then) did not refuse a positional argument it
// had no name for — it discarded it, so the program ran, the tool was called
// with {}, and what came back was a complaint about a missing path for a call
// that plainly supplied one. Nothing in the harness said "positional arguments
// do not work", which is the property this pins. The options object is the
// documented convention now; positionals, and a trailing options object after
// them, still bind (jsArgs).
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
			name:   "an options object mixes with positionals",
			code:   `read("README.md", {limit: 1})`,
			want:   []string{"alpha"},
			absent: "beta",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(c, "console.log("+tc.code+")")
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
// the tool answers it rather than the bridge swallowing it. Binding the
// parameter names must not turn the bridge into a second validator with its own
// idea of what each tool accepts.
func TestUnknownKeywordsStillReachTheTool(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"README.md": "alpha\n"})
	got := run(c, `console.log(read({path: "README.md", nope: 1}))`)
	if !strings.Contains(got, "alpha") {
		t.Errorf("an unknown keyword stopped a valid call from working:\n%s", got)
	}
}

package repl

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
)

// TestCommandArgsNotation holds the help table to one notation. The convention
// is documented beside the command struct; this checks the table obeys it,
// because a convention nothing enforces is how the table drifted into four
// notations in the first place — NAME..., [file ...], [generate|drop], and
// <file> [file ...] all meant the same thing on the same screen.
func TestCommandArgsNotation(t *testing.T) {
	for _, c := range commands {
		if c.args == "" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			checkNotation(t, c.args)
		})
	}
}

// checkNotation walks one args string token by token. It is deliberately a
// scanner over the rendered text rather than a grammar the table is built from:
// what /help prints is what a user reads, so that is what has to be checked.
func checkNotation(t *testing.T, args string) {
	t.Helper()

	depth := 0
	tokens := strings.Fields(args)
	for i, tok := range tokens {
		bare := tok
		for strings.HasPrefix(bare, "[") {
			depth++
			bare = bare[1:]
		}
		closes := 0
		for strings.HasSuffix(bare, "]") {
			closes++
			bare = bare[:len(bare)-1]
		}
		depth -= closes
		if depth < 0 {
			t.Fatalf("%q: unbalanced brackets", args)
		}

		switch {
		case bare == "...":
			if i == 0 {
				t.Errorf("%q: %q has nothing before it to repeat", args, tok)
			}
		case bare == "|":
			// An alternation bar, which the /env and /notes rows use.
		case strings.HasPrefix(bare, "<"):
			if !strings.HasSuffix(bare, ">") {
				t.Errorf("%q: %q opens a metavariable it does not close", args, tok)
				continue
			}
			name := bare[1 : len(bare)-1]
			if name == "" || name != strings.ToLower(name) {
				t.Errorf("%q: %q should be a lowercase metavariable name", args, tok)
			}
			if strings.ContainsAny(name, " \t") {
				t.Errorf("%q: %q should be one word", args, tok)
			}
		default:
			// A literal keyword: /env's add, drop, reset; /notes' generate and
			// drop; /symbol's definition and reference. Lowercase, no brackets
			// of its own.
			if bare != strings.ToLower(bare) || strings.ContainsAny(bare, "<>[]") {
				t.Errorf("%q: %q is neither a metavariable nor a plain keyword", args, tok)
			}
		}
	}
	if depth != 0 {
		t.Fatalf("%q: unbalanced brackets", args)
	}
}

// TestCommandsAreSorted keeps the help screen scannable. /sandbox had drifted
// two rows out of place, which nobody notices while reading the source — the
// table is a list of structs there, and a list of 28 lines only on screen.
func TestCommandsAreSorted(t *testing.T) {
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.name
	}
	if !slices.IsSorted(names) {
		sorted := slices.Clone(names)
		slices.Sort(sorted)
		t.Errorf("commands are out of order:\n got %v\nwant %v", names, sorted)
	}
}

// TestRestOfLineArgsComeLast is the claim that makes the notation work without
// a fourth mark: an argument that swallows the rest of the line is necessarily
// the last one, and cannot repeat. If a command ever wants two of them, or
// wants one followed by something else, the convention needs a real mark and
// this test is where that will surface.
func TestRestOfLineArgsComeLast(t *testing.T) {
	for _, c := range commands {
		tokens := strings.Fields(c.args)
		for i, tok := range tokens {
			name := strings.Trim(tok, "[]<>")
			if !slices.Contains(restOfLine, name) || !strings.Contains(tok, "<") {
				continue
			}
			if i != len(tokens)-1 {
				t.Errorf("/%s %q: <%s> takes the rest of the line, so nothing can follow it", c.name, c.args, name)
			}
			if strings.Contains(c.args, "...") {
				t.Errorf("/%s %q: <%s> takes the rest of the line, so it cannot repeat", c.name, c.args, name)
			}
		}
	}
}

// TestUsageMatchesHelp pins the other half of the drift: the message a command
// prints when its arguments are wrong is built from the table, so a reader who
// gets it wrong twice sees the same syntax both times.
func TestUsageMatchesHelp(t *testing.T) {
	got := usage("add")
	want := "Usage: /add " + findCommand("add").args
	if got != want {
		t.Errorf("usage(\"add\") = %q, want %q", got, want)
	}
	// A command with no arguments has no syntax to quote, and asking for one
	// must not produce a line ending in a stray space.
	if got := usage("help"); got != "Usage: /help" {
		t.Errorf("usage(\"help\") = %q", got)
	}
}

// TestPinMessagesMatchLs is the harmonization: the name /add and /read-only
// print for a file is the name /ls, the banner, and the per-prompt header use
// for it. They disagreed for anything outside the project — /read-only echoed
// an absolute path, everything else the root-relative one — so a sibling
// repository's header appeared under two names in the same session.
func TestPinMessagesMatchLs(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	proj := filepath.Join(root, "proj")
	for _, d := range []string{outside, proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(proj, "a.go"), filepath.Join(outside, "spec.md")} {
		if err := os.WriteFile(f, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	input := strings.NewReader("/add a.go\n/read-only ../outside/spec.md\n/ls\n/exit\n")
	cdr := coder.New(proj, testModel())
	cdr.Client = answerStub("ok\n")
	out := &syncBuffer{}
	r, err := New(Options{
		Coder: cdr, Config: testConfig(cdr.Model), ModelAlias: "test",
		Stdin: input, Stdout: out, Stderr: out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := out.String()
	// Every name the session printed for the out-of-tree pin must be the one
	// /ls lists, which is also the only one a tool call can use: contain
	// refuses an absolute path before the pinned-file exemption.
	want := "../outside/spec.md"
	if n := strings.Count(got, "Pinned "+want+" read-only."); n != 1 {
		t.Errorf("confirmation should name the file as %q:\n%s", want, got)
	}
	if strings.Contains(got, "Pinned "+outside) {
		t.Errorf("confirmation used an absolute path:\n%s", got)
	}
	for _, ro := range cdr.ReadOnlyFiles() {
		if ro != want {
			t.Errorf("/ls form = %q, want %q", ro, want)
		}
	}
}

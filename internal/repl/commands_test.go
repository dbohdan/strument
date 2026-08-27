package repl

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/repomap"
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
// an absolute path, everything else counted back to the file with ../.. — so
// one pin appeared under two names in the same session.
//
// The assertion is congruence, and it is made on both sides of the display
// threshold, since a rule with a branch in it can be congruent on one side and
// not the other.
func TestPinMessagesMatchLs(t *testing.T) {
	tests := []struct {
		name    string
		depth   []string // project root, under the temp dir
		wantAbs bool
	}{
		{name: "sibling stays relative", depth: []string{"proj"}},
		{name: "distant is absolute", depth: []string{"a", "b", "proj"}, wantAbs: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(root, "outside")
			proj := filepath.Join(append([]string{root}, tt.depth...)...)
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

			cdr := coder.New(proj, testModel())
			cdr.Client = answerStub("ok\n")
			out := &syncBuffer{}
			input := strings.NewReader("/add a.go\n/read-only " + filepath.Join(outside, "spec.md") + "\n/ls\n/exit\n")
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

			// /ls is the reference: whatever it lists is the file's name.
			ro := cdr.ReadOnlyFiles()
			if len(ro) != 1 {
				t.Fatalf("ReadOnlyFiles() = %v, want one entry", ro)
			}
			name := ro[0]
			if got := filepath.IsAbs(name); got != tt.wantAbs {
				t.Errorf("pin listed as %q, absolute = %v, want %v", name, got, tt.wantAbs)
			}

			got := out.String()
			if n := strings.Count(got, "Pinned "+name+" read-only."); n != 1 {
				t.Errorf("confirmation should name the file %q:\n%s", name, got)
			}
			// The in-tree pin stays root-relative on both sides: the project's
			// own files are named the way every tool result names them.
			if chat := cdr.ChatFiles(); len(chat) != 1 || chat[0] != "a.go" {
				t.Errorf("ChatFiles() = %v, want [a.go]", chat)
			}
			if !strings.Contains(got, "Pinned a.go.") {
				t.Errorf("in-tree confirmation should be root-relative:\n%s", got)
			}
		})
	}
}

// TestQuotingAppliesToFileArgumentsOnly pins the line the notation comment used
// to draw in the wrong place. It claimed every word-shaped argument is split on
// whitespace and so quotable; only the four file commands tokenize, and the
// rest take the trimmed remainder — quotes included.
//
// Both directions are asserted. Which commands quote is a real boundary, and a
// test that only checked the working half would let someone move it by accident
// in either direction.
func TestQuotingAppliesToFileArgumentsOnly(t *testing.T) {
	spaced := "my file.txt"

	t.Run("a file argument is tokenized", func(t *testing.T) {
		r, cdr, out := newTestREPL(t, answerStub("ok\n"), strings.NewReader(
			`/add "`+spaced+`"`+"\n/ls\n/exit\n"))
		defer r.Close()
		if err := os.WriteFile(filepath.Join(cdr.Root, spaced), []byte("hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := r.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := cdr.ChatFiles(); len(got) != 1 || got[0] != spaced {
			t.Errorf("ChatFiles() = %v, want [%q]; the quotes should not survive", got, spaced)
		}
		if !strings.Contains(out.String(), "Pinned "+spaced+".") {
			t.Errorf("confirmation should name the file without quotes:\n%s", out.String())
		}
	})

	// The counter-half. /model is the representative: it uses the argument as a
	// map key, so a stray quote shows up verbatim in the error and there is no
	// ambiguity about what it received.
	t.Run("a non-file argument is not", func(t *testing.T) {
		r, _, out := newTestREPL(t, &fixture.StreamStub{}, strings.NewReader("/model \"test\"\n/exit\n"))
		defer r.Close()
		if err := r.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if strings.Contains(got, "Switched to model") {
			t.Errorf("/model tokenized its argument; the notation comment says it does not:\n%s", got)
		}
		if !strings.Contains(got, `Unknown model alias`) {
			t.Errorf("expected the quotes to reach the lookup verbatim:\n%s", got)
		}
	})
}

// TestDropTakesAnAbsolutePath closes a gap the Windows work opened: splitArgs
// used to eat the separators out of C:\proj\a.go, so no path command could take
// an absolute path there at all. /add and /read-only are covered by
// TestPinMessagesMatchLs; /drop was not covered anywhere.
func TestDropTakesAnAbsolutePath(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("ok\n"), nil)
	defer r.Close()
	abs := filepath.Join(cdr.Root, "hello.txt")
	cdr.AddFile(abs)
	if len(cdr.ChatFiles()) != 1 {
		t.Fatalf("fixture did not pin: %v", cdr.ChatFiles())
	}

	cmdDrop(context.Background(), r, abs)
	if got := cdr.ChatFiles(); len(got) != 0 {
		t.Errorf("an absolute path did not drop the pin: still %v\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "Unpinned hello.txt.") {
		t.Errorf("the message should name the file the way /ls does:\n%s", out.String())
	}
}

// TestSymbolAcceptsBothKinds covers a value /help advertises. "definition" was
// added to the help line when it turned out the tool had always accepted it and
// only "reference" was shown — a value advertised and never exercised.
//
// The parser has to be wired up for this to mean anything. SymbolLookup checks
// for it before it validates the kind, so a coder without a RepoMap answers
// "the language parser is not available" for every kind, valid or not: the
// first version of this test asserted the absence of "Unknown kind" and passed
// with "definition" deleted from the accepted set.
//
// So it asserts the answer rather than the absence of a complaint. definition
// and the bare form must find the definition; reference must find the use, and
// the two must not return the same thing — otherwise the kind is being ignored.
func TestSymbolAcceptsBothKinds(t *testing.T) {
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, nil)
	defer r.Close()
	if err := os.WriteFile(filepath.Join(cdr.Root, "lib.go"), []byte(
		"package lib\n\nfunc VerySpecificName() int { return 1 }\n\nfunc caller() int { return VerySpecificName() }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	cdr.RepoMap = repomap.New(cdr.Root)

	ask := func(args string) string {
		out.Reset()
		cmdSymbol(context.Background(), r, args)
		got := out.String()
		if strings.Contains(got, "language parser is not available") {
			t.Fatalf("no parser, so this test cannot see a rejected kind: %s", got)
		}
		return got
	}

	bare := ask("VerySpecificName")
	definition := ask("VerySpecificName definition")
	reference := ask("VerySpecificName reference")

	for name, got := range map[string]string{"bare": bare, "definition": definition} {
		if !strings.Contains(got, "is defined in") {
			t.Errorf("%s should report the definition:\n%s", name, got)
		}
	}
	if bare != definition {
		t.Errorf("an omitted kind and \"definition\" should agree:\n%s\n---\n%s", bare, definition)
	}
	// Line numbers, not prose. The header is worded from the kind while the
	// sites come from what the kind was translated into, so a lookup that
	// ignored the kind entirely would still say "referenced" over the
	// definition's line — checked by breaking it exactly that way.
	if !strings.Contains(definition, "lib.go:3") {
		t.Errorf("definition should point at line 3, where it is defined:\n%s", definition)
	}
	if !strings.Contains(reference, "is referenced in") || !strings.Contains(reference, "lib.go:5") {
		t.Errorf("reference should point at line 5, where it is called:\n%s", reference)
	}
	if strings.Contains(reference, "lib.go:3") {
		t.Errorf("reference returned the definition's site; the kind is being ignored:\n%s", reference)
	}
}

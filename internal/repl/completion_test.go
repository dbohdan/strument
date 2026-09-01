package repl

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
)

func runeStrings(rs [][]rune) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

// TestCompleteWord exercises the pure word matcher: the token under the cursor,
// prefix matching against paths and basenames, and the returned suffix + length.
func TestCompleteWord(t *testing.T) {
	cands := []string{
		"README.md",
		"internal/coder/scrape.go",
		"internal/coder/session.go",
		"scrape.go",
		"session.go",
	}
	tests := []struct {
		name    string
		line    string
		pos     int // -1 => end of line
		want    []string
		wantLen int
	}{
		{"prefix single", "READ", -1, []string{"ME.md"}, 4},
		{"basename multi", "s", -1, []string{"crape.go", "ession.go"}, 1},
		{"path segment", "internal/co", -1, []string{"der/scrape.go", "der/session.go"}, 11},
		{"no match", "zzz", -1, nil, 3},
		{"empty token after space", "look at ", -1, nil, 0},
		{"mid-line word", "see READ here", 8, []string{"ME.md"}, 4},
		{"case sensitive", "readme", -1, nil, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := []rune(tt.line)
			pos := tt.pos
			if pos < 0 {
				pos = len(line)
			}
			got, gotLen := completeWord(line, pos, cands)
			if !slices.Equal(runeStrings(got), tt.want) {
				t.Errorf("suffixes = %v, want %v", runeStrings(got), tt.want)
			}
			if gotLen != tt.wantLen {
				t.Errorf("length = %d, want %d", gotLen, tt.wantLen)
			}
		})
	}
}

type stubCompleter struct{ called *bool }

func (s stubCompleter) Do(_ []rune, _ int) ([][]rune, int) {
	*s.called = true
	return [][]rune{[]rune("SENTINEL")}, 0
}

// TestPromptCompleterRouting: a "/"-line goes to the command completer; a bare
// line goes to file-path completion.
func TestPromptCompleterRouting(t *testing.T) {
	cmdCalled := false
	pc := promptCompleter{
		cmd:   stubCompleter{called: &cmdCalled},
		files: func() []string { return []string{"README.md"} },
	}

	got, _ := pc.Do([]rune("/add REA"), 8)
	if !cmdCalled {
		t.Error("a slash line should route to the command completer")
	}
	if len(got) != 1 || string(got[0]) != "SENTINEL" {
		t.Errorf("command completion = %v, want [SENTINEL]", runeStrings(got))
	}

	cmdCalled = false
	got, n := pc.Do([]rune("REA"), 3)
	if cmdCalled {
		t.Error("a bare line should not route to the command completer")
	}
	if len(got) != 1 || string(got[0]) != "DME.md" || n != 3 {
		t.Errorf("file completion = %v len=%d, want [DME.md] len=3", runeStrings(got), n)
	}
}

func TestDropCompletionIncludesReadOnlyFiles(t *testing.T) {
	r, cdr, _ := newTestREPL(t, &fixture.StreamStub{}, strings.NewReader("/exit\n"))
	defer r.Close()
	cdr.AddReadOnlyFile("hello.txt")

	got := completionsFor(r.completer(), "/drop hel")
	if len(got) != 1 || got[0] != "lo.txt" {
		t.Errorf("/drop completion = %v, want [lo.txt] for the read-only pin", got)
	}
}

// TestPathCommandCompletion pins the /read-only and /submit argument
// completer: arbitrary filesystem paths, absolute included — the point of
// /read-only is material outside the project, which completeAddable's flat
// listing of the root could never offer.
func TestPathCommandCompletion(t *testing.T) {
	r, _, _ := newTestREPL(t, &fixture.StreamStub{}, strings.NewReader("/exit\n"))
	defer r.Close()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "prompt.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outside, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ line, want string }{
		// Absolute word: completes the file; a directory slash-descends.
		{"/submit " + filepath.Join(outside, "pro"), "mpt.md"},
		// Project-relative word.
		{"/submit hel", "lo.txt"},
		{"/read-only hel", "lo.txt"},
	} {
		got := completionsFor(r.completer(), tc.line)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q = %q, want [%q]", tc.line, got, tc.want)
		}
	}

	// A trailing slash lists the directory: both entries, the directory one
	// with its slash so the next Tab descends.
	slash := outside + string(filepath.Separator)
	got := completionsFor(r.completer(), "/read-only "+slash)
	if len(got) != 2 || got[0] != "prompt.md" || got[1] != "subdir/" {
		t.Errorf("/read-only %s = %q, want [prompt.md subdir/]", slash, got)
	}
}

// TestPathCompletionMultiSegmentDescends pins the multi-segment case: a
// directory prefix typed in the word is honored, which the old flat-root
// completer could not do.
func TestPathCompletionMultiSegmentDescends(t *testing.T) {
	r, _, _ := newTestREPL(t, &fixture.StreamStub{}, strings.NewReader("/exit\n"))
	defer r.Close()

	if err := os.Mkdir(filepath.Join(r.coder.Root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.coder.Root, "docs", "spec.md"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := completionsFor(r.completer(), "/read-only docs/s")
	if len(got) != 1 || got[0] != "pec.md" {
		t.Errorf("/read-only docs/s = %q, want [pec.md]", got)
	}
}

// TestPathCompletionSpaceEscapes pins the quoting contract: a path with a
// space completes with the space escaped, so what Tab inserts re-parses via
// splitArgs to the same path.
func TestPathCompletionSpaceEscapes(t *testing.T) {
	r, _, _ := newTestREPL(t, &fixture.StreamStub{}, strings.NewReader("/exit\n"))
	defer r.Close()

	if err := os.WriteFile(filepath.Join(r.coder.Root, "my file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := completionsFor(r.completer(), "/submit my")
	if backslashEscapes {
		if len(got) != 1 || got[0] != `\ file.txt` {
			t.Errorf("/submit my = %q, want escaped-space file.txt", got)
		}
	} else {
		// Windows: no backslash escapes, so no candidate can match the raw
		// buffer (the space would end the word) and completion stays silent.
		if len(got) != 0 {
			t.Errorf("/submit my on windows = %q, want none", got)
		}
	}
}

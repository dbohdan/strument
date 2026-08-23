package repl

import (
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

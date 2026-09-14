package shlex

import (
	"runtime"
	"slices"
	"testing"
)

// TestSplit covers what both platforms agree on. The two cases that used to
// live here — an unquoted backslash escaping the next rune — moved to
// TestSplitBackslashPerPlatform when that stopped being universal: they were
// written as facts about Split and were really facts about Unix, so Windows
// CI failed on the assertions rather than on the behavior.
func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a b c", []string{"a", "b", "c"}},
		{"  a   b  ", []string{"a", "b"}},
		{`"my file.txt"`, []string{"my file.txt"}},
		{`'my file.txt'`, []string{"my file.txt"}},
		{`one "two words" three`, []string{"one", "two words", "three"}},
		{`dir/"a b".go`, []string{"dir/a b.go"}},
		{`"esc \" quote"`, []string{`esc " quote`}},
		{`'no \ escape'`, []string{`no \ escape`}}, // backslash literal in single quotes
		{`"unterminated`, []string{"unterminated"}},
	}
	for _, tc := range cases {
		got := Split(tc.in)
		if !slices.Equal(got, tc.want) {
			t.Errorf("Split(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSplitBackslashPerPlatform exercises both platforms' rules from
// whichever host runs the test. That is the point of the SplitWith seam:
// the Windows rule was wrong for as long as it existed and no Unix CI run could
// have said so.
func TestSplitBackslashPerPlatform(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		escapes bool
		want    []string
	}{
		{
			name:    "unix: a backslash escapes a space",
			in:      `my\ file.txt`,
			escapes: true,
			want:    []string{"my file.txt"},
		},
		{
			// The bug. A Windows path lost every separator and matched nothing,
			// so /add and /read-only could not take an absolute path at all.
			name:    "windows: a path keeps its separators",
			in:      `C:\Users\me\spec.md`,
			escapes: false,
			want:    []string{`C:\Users\me\spec.md`},
		},
		{
			name:    "windows: spaces still need quotes",
			in:      `"C:\Program Files\x.txt"`,
			escapes: false,
			want:    []string{`C:\Program Files\x.txt`},
		},
		{
			// Quoting is the portable spelling, so it has to mean the same
			// thing under both rules.
			name:    "unix: quoting a path with spaces",
			in:      `"/home/me/my file.txt"`,
			escapes: true,
			want:    []string{"/home/me/my file.txt"},
		},
		{
			name:    "windows: two paths split on whitespace",
			in:      `C:\a\b.txt D:\c\d.txt`,
			escapes: false,
			want:    []string{`C:\a\b.txt`, `D:\c\d.txt`},
		},
		{
			// Moved from TestSplit, where it read as a fact about Split
			// and was a fact about Unix.
			name:    "unix: bare backslashes escape the next rune",
			in:      `path\with\backslash`,
			escapes: true,
			want:    []string{"pathwithbackslash"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitWith(tt.in, tt.escapes)
			if !slices.Equal(got, tt.want) {
				t.Errorf("SplitWith(%q, %v) = %q, want %q", tt.in, tt.escapes, got, tt.want)
			}
		})
	}
}

// TestSplitUsesThePlatformRule ties the seam to the real thing, so the
// tested function and the used one cannot drift apart.
func TestSplitUsesThePlatformRule(t *testing.T) {
	in := `a\ b`
	if got, want := Split(in), SplitWith(in, BackslashEscapes); !slices.Equal(got, want) {
		t.Errorf("Split(%q) = %q, want %q", in, got, want)
	}
	if BackslashEscapes != (runtime.GOOS != "windows") {
		t.Errorf("BackslashEscapes = %v on %s", BackslashEscapes, runtime.GOOS)
	}
}

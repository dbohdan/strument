package repl

import (
	"runtime"
	"slices"
	"testing"
)

func TestSplitArgs(t *testing.T) {
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
		{`a\ b`, []string{"a b"}},
		{`one "two words" three`, []string{"one", "two words", "three"}},
		{`dir/"a b".go`, []string{"dir/a b.go"}},
		{`"esc \" quote"`, []string{`esc " quote`}},
		{`'no \ escape'`, []string{`no \ escape`}}, // backslash literal in single quotes
		{`"unterminated`, []string{"unterminated"}},
		{`path\with\backslash`, []string{"pathwithbackslash"}}, // bare backslashes escape the next rune
	}
	for _, tc := range cases {
		got := splitArgs(tc.in)
		if !slices.Equal(got, tc.want) {
			t.Errorf("splitArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSplitArgsBackslashPerPlatform exercises both platforms' rules from
// whichever host runs the test. That is the point of the splitArgsWith seam:
// the Windows rule was wrong for as long as it existed and no Unix CI run could
// have said so.
func TestSplitArgsBackslashPerPlatform(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitArgsWith(tt.in, tt.escapes)
			if !slices.Equal(got, tt.want) {
				t.Errorf("splitArgsWith(%q, %v) = %q, want %q", tt.in, tt.escapes, got, tt.want)
			}
		})
	}
}

// TestSplitArgsUsesThePlatformRule ties the seam to the real thing, so the
// tested function and the used one cannot drift apart.
func TestSplitArgsUsesThePlatformRule(t *testing.T) {
	in := `a\ b`
	if got, want := splitArgs(in), splitArgsWith(in, backslashEscapes); !slices.Equal(got, want) {
		t.Errorf("splitArgs(%q) = %q, want %q", in, got, want)
	}
	if backslashEscapes != (runtime.GOOS != "windows") {
		t.Errorf("backslashEscapes = %v on %s", backslashEscapes, runtime.GOOS)
	}
}

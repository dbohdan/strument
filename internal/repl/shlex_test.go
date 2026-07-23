package repl

import (
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

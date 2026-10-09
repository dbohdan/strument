package gitrepo

import (
	"strings"
	"testing"
)

func TestDiffListings(t *testing.T) {
	rec := func(recs ...string) string { return strings.Join(recs, "\x00") + "\x00" }
	a := "100644 aaa 0\ta.txt"
	a2 := "100644 a22 0\ta.txt"
	b := "100644 bbb 0\tb.txt"
	c1, c2, c3 := "100644 c1 1\tc.txt", "100644 c2 2\tc.txt", "100644 c3 3\tc.txt"
	c0 := "100644 c0 0\tc.txt"
	d := "100644 ddd 0\td/e.txt"
	for _, tc := range []struct {
		name, before, after, want string
	}{
		{"same", rec(a, b), rec(a, b), ""},
		{"both empty", "", "", ""},
		{"from empty", "", rec(a, b), "a.txt,b.txt"},
		{"to empty", rec(a, b), "", "a.txt,b.txt"},
		{"changed blob", rec(a, b), rec(a2, b), "a.txt"},
		{"rename", rec(a, d), rec(b, d), "a.txt,b.txt"},
		{"added in the middle", rec(a, d), rec(a, b, d), "b.txt"},
		{"removed at the end", rec(a, b, d), rec(a, b), "d/e.txt"},
		{"conflict resolved", rec(a, c1, c2, c3, d), rec(a, c0, d), "c.txt"},
		{"conflict appears", rec(a, c0), rec(a, c1, c2, c3), "c.txt"},
	} {
		got := strings.Join(diffListings(tc.before, tc.after), ",")
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

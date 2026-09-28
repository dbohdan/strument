package history

import (
	"testing"
)

func TestCompareNatural(t *testing.T) {
	ordered := []string{"foo", "foo-2", "foo-9", "foo-10", "foo-010", "foo-11", "foo-a", "foo2", "zeta"}
	for i := range ordered {
		for j := range ordered {
			want := cmpInt(i, j)
			if got := compareNatural(ordered[i], ordered[j]); got != want {
				t.Errorf("compareNatural(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

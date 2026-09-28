package history

import (
	"strings"
)

// compareNatural orders names with digit runs compared as numbers, so
// "foo-9" sorts before "foo-10". Runs of digits compare by value, then by
// length (so "foo-02" follows "foo-2"), and everything else byte by byte.
func compareNatural(a, b string) int {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da > 0 && db > 0 {
			na := strings.TrimLeft(a[:da], "0")
			nb := strings.TrimLeft(b[:db], "0")
			if len(na) != len(nb) {
				return cmpInt(len(na), len(nb))
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			if da != db {
				return cmpInt(da, db)
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return cmpInt(int(a[0]), int(b[0]))
		}
		a, b = a[1:], b[1:]
	}
	return cmpInt(len(a), len(b))
}

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

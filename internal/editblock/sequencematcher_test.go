package editblock

import (
	"math"
	"testing"
)

// Parity values pinned against CPython 3.11 difflib on 2026-07-16.
func TestStringRatioParity(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"file1_py", "file1.py", 0.875},
		{`\windows__init__.py`, `\windows\__init__.py`, 0.9743589743589743},
		{"abcdef", "abcdef", 1.0},
		{"", "", 1.0},
		{"abc", "", 0.0},
		{"kitten", "sitting", 0.6153846153846154},
		{"the quick brown fox", "the quick brown dog", 0.8947368421052632},
	}
	for _, c := range cases {
		got := stringRatio(c.a, c.b)
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("ratio(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestLineRatioParity(t *testing.T) {
	if got := lineRatio(
		[]string{"def foo():", "    return 1", ""},
		[]string{"def foo():", "    return 2", ""},
	); math.Abs(got-2.0/3.0) > 1e-12 {
		t.Errorf("lines1 = %v", got)
	}
	if got := lineRatio(
		[]string{"x=1", "y=2", "z=3"},
		[]string{"y=2", "z=3", "w=4"},
	); math.Abs(got-2.0/3.0) > 1e-12 {
		t.Errorf("lines2 = %v", got)
	}
}

// The autojunk heuristic must engage at len(b) >= 200 exactly as difflib's:
// popular elements can't seed matches. Pinned against CPython.
func TestAutojunkParity(t *testing.T) {
	rep := func(s string, n int) string {
		out := ""
		for range n {
			out += s
		}
		return out
	}
	long := stringRatio(rep("ab", 150)+"Q", "Q"+rep("ab", 150))
	if math.Abs(long-0.0033222591362126247) > 1e-12 {
		t.Errorf("autojunk long = %v", long)
	}
	short := stringRatio(rep("ab", 80)+"Q", "Q"+rep("ab", 80))
	if math.Abs(short-0.9937888198757764) > 1e-12 {
		t.Errorf("short (no autojunk) = %v", short)
	}
}

func TestGetCloseMatches1(t *testing.T) {
	valid := []string{"file1.py", "file2.py", "dir/file3.py", `\windows\__init__.py`}
	if m, ok := getCloseMatches1("file1_py", valid, 0.8); !ok || m != "file1.py" {
		t.Errorf("got %q, %v", m, ok)
	}
	if _, ok := getCloseMatches1("zzz", valid, 0.8); ok {
		t.Error("want no match for zzz")
	}
	// Tie-break toward the lexicographically larger string, per
	// heapq.nlargest over (ratio, string) tuples.
	if m, ok := getCloseMatches1("apple", []string{"ample", "apples"}, 0.5); !ok || m != "apples" {
		t.Errorf("tie-break got %q (want apples)", m)
	}
}

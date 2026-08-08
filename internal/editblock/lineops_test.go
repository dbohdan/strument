package editblock

import "testing"

// Parity values pinned against CPython 3.11 difflib.SequenceMatcher.get_opcodes
// on 2026-08-08, the same way the ratio tests above pin ratio().
func TestLineOpsParity(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want []Op
	}{
		{
			name: "one line inside context",
			a:    []string{"func foo() {", "  x := 1", "  return x", "}"},
			b:    []string{"func foo() {", "  x := 2", "  return x", "}"},
			want: []Op{{OpEqual, 0, 1, 0, 1}, {OpReplace, 1, 2, 1, 2}, {OpEqual, 2, 4, 2, 4}},
		},
		{
			name: "insert into nothing",
			a:    nil,
			b:    []string{"a", "b"},
			want: []Op{{OpInsert, 0, 0, 0, 2}},
		},
		{
			name: "delete everything",
			a:    []string{"a", "b"},
			b:    nil,
			want: []Op{{OpDelete, 0, 2, 0, 0}},
		},
		{
			name: "identical",
			a:    []string{"a", "b", "c"},
			b:    []string{"a", "b", "c"},
			want: []Op{{OpEqual, 0, 3, 0, 3}},
		},
		{
			name: "delete in the middle",
			a:    []string{"a", "b", "c", "d"},
			b:    []string{"a", "d"},
			want: []Op{{OpEqual, 0, 1, 0, 1}, {OpDelete, 1, 3, 1, 1}, {OpEqual, 3, 4, 1, 2}},
		},
		{
			name: "shifted window",
			a:    []string{"x=1", "y=2", "z=3"},
			b:    []string{"y=2", "z=3", "w=4"},
			want: []Op{{OpDelete, 0, 1, 0, 0}, {OpEqual, 1, 3, 0, 2}, {OpInsert, 3, 3, 2, 3}},
		},
		{
			name: "replace then append",
			a:    []string{"one", "two", "three"},
			b:    []string{"one", "2", "three", "four"},
			want: []Op{
				{OpEqual, 0, 1, 0, 1}, {OpReplace, 1, 2, 1, 2},
				{OpEqual, 2, 3, 2, 3}, {OpInsert, 3, 3, 3, 4},
			},
		},
		{
			name: "both empty",
			a:    nil,
			b:    nil,
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LineOps(c.a, c.b)
			if len(got) != len(c.want) {
				t.Fatalf("LineOps = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("op %d = %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// Whatever the ops are, they must tile both inputs end to end: the renderer
// walks them as the whole story of the change and would silently drop lines
// from a gap.
func TestLineOpsCoverInput(t *testing.T) {
	a := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	b := []string{"alpha", "BETA", "gamma", "epsilon", "zeta"}
	i, j := 0, 0
	for _, op := range LineOps(a, b) {
		if op.A1 != i || op.B1 != j {
			t.Fatalf("gap before %v: at (%d, %d)", op, i, j)
		}
		i, j = op.A2, op.B2
	}
	if i != len(a) || j != len(b) {
		t.Errorf("ops stopped at (%d, %d), want (%d, %d)", i, j, len(a), len(b))
	}
}

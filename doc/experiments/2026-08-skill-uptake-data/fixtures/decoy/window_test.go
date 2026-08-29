package window

import (
	"reflect"
	"testing"
)

func TestSliceReturnsExactlyN(t *testing.T) {
	w := New(5)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		w.Push(v)
	}
	for _, test := range []struct {
		n    int
		want []float64
	}{
		{1, []float64{5}},
		{2, []float64{4, 5}},
		{3, []float64{3, 4, 5}},
		{5, []float64{1, 2, 3, 4, 5}},
		{9, []float64{1, 2, 3, 4, 5}},
	} {
		if got := w.Slice(test.n); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Slice(%d) = %v, want %v", test.n, got, test.want)
		}
	}
}

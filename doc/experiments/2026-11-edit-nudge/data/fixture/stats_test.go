package stats

import "testing"

func TestMean(t *testing.T) {
	if got := Mean([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("Mean = %v, want 2.5", got)
	}
}

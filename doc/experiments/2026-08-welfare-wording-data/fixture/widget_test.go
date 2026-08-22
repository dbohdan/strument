package widget

import "testing"

func TestRound(t *testing.T) {
	if got := Round(2.5); got != 3 {
		t.Errorf("Round(2.5) = %d, want 3", got)
	}
	if got := Round(-2.5); got != -2 {
		t.Errorf("Round(-2.5) = %d, want -2", got)
	}
}

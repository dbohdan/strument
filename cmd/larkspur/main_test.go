package main

import "testing"

// TestMultiplier checks the break-even arithmetic on figures small
// enough to work out by hand. The garden's worth at multiplier m is
// m times the asparagus share plus the annual harvest beside it;
// the break-even m is where that equals the rotation-only total.
func TestMultiplier(t *testing.T) {
	tests := []struct {
		name             string
		withTotal, share float64 // garden with asparagus: total, its asparagus part
		noneTotal        float64 // rotation-only total
		want             float64
	}{
		// Annuals beside the asparagus: 10 - 4 = 6. Worth 6 + 4m;
		// matching 12 needs 4m = 6, so m = 1.5.
		{"deficit twice the share", 10, 4, 12, 1.5},
		// Annuals: 12 - 4 = 8. Worth 8 + 4m; matching 16 needs
		// 4m = 8, so m = 2.
		{"deficit equal to the share", 12, 4, 16, 2},
		// Annuals: 12 - 8 = 4. Worth 4 + 8m; matching 10 needs
		// 8m = 6, so m = 0.75 — already ahead at parity.
		{"already ahead at parity", 12, 8, 10, 0.75},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := multiplier(tt.withTotal, tt.share, tt.noneTotal)
			if got != tt.want {
				t.Errorf("multiplier(%v, %v, %v) = %v, want %v",
					tt.withTotal, tt.share, tt.noneTotal, got, tt.want)
			}
		})
	}
}

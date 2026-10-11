package main

import (
	"fmt"
	"io"
	"os"
	"testing"
)

// captureStdout runs f with os.Stdout pointed at a temporary
// file and returns what f printed, so a test can compare
// output byte for byte.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	tmp, err := os.CreateTemp("", "larkspur-stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	old := os.Stdout
	os.Stdout = tmp
	f()
	os.Stdout = old

	if _, err := tmp.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(tmp)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDefaultOutputUnchanged checks that the flags that take
// a potassium dressing — -dressK on the default table and
// -paired, -orderK on -order — leave the default output
// exactly as it was before they existed. With both flags at
// their defaults, main runs the same code the pre-flag
// wrappers run; this checks the two print the same bytes.
// Few seeds are fine: the comparison is like for like.
func TestDefaultOutputUnchanged(t *testing.T) {
	const seasons, seeds, beds = 6, 5, 6
	const first = int64(0)

	// newGarden is the constructor main builds from the
	// flags at their defaults, so this checks main's own
	// wiring, not a garden constructed independently here.
	newGarden := gardenAt(1, beds)

	// The default table, with its sparklines.
	now := captureStdout(t, func() {
		rows := measureWith(newGarden, seasons, seeds, first, nil)
		printTable(rows)
		printSparklines(rows)
	})
	before := captureStdout(t, func() {
		rows := measure(seasons, seeds, beds, first, nil)
		printTable(rows)
		printSparklines(rows)
	})
	if now != before {
		t.Errorf("default table changed with -dressK at its default\nnew:\n%s\nold:\n%s", now, before)
	}

	// -paired.
	now = captureStdout(t, func() {
		pairedCompareWith(newGarden, seasons, seeds, first)
	})
	before = captureStdout(t, func() {
		pairedCompare(seasons, seeds, beds, first)
	})
	if now != before {
		t.Errorf("-paired changed with -dressK at its default\nnew:\n%s\nold:\n%s", now, before)
	}

	// -order. At its default -orderK, main calls orderRank —
	// the function that existed before -orderK — which is
	// orderRankWith on a plain garden with the plain header.
	// Check that construction still matches.
	header := fmt.Sprintf("every cyclic order (brassicas first) ranked by mean total over %d shared seeds", seeds)
	now = captureStdout(t, func() {
		orderRankWith(newGarden, header, seasons, seeds, first)
	})
	before = captureStdout(t, func() {
		orderRank(seasons, seeds, beds, first)
	})
	if now != before {
		t.Errorf("-order changed with -orderK at its default\nnew:\n%s\nold:\n%s", now, before)
	}
}

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

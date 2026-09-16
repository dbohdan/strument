package coder

import (
	"strings"
	"testing"
	"time"
)

// The rate on screen and the rate in the two files are one measurement, and the
// rule about when it exists is one rule. Three renderings is three chances for
// two of them to disagree about whether 40ms of stream counts as a rate.
func TestTokenRateValueAndLineAgree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		received int
		elapsed  time.Duration
		want     float64
	}{
		{"a normal turn", 1000, 5 * time.Second, 200},
		{"rounded to two decimals", 1000, 3 * time.Second, 333.33},
		{"a slow model keeps its first decimal on screen", 9, 2 * time.Second, 4.5},
		{"nothing received", 0, 5 * time.Second, 0},
		{"too little time to divide by", 1000, 10 * time.Millisecond, 0},
		{"a fake clock, as tests have", 1000, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenRateValue(tc.received, tc.elapsed)
			if got != tc.want {
				t.Errorf("tokenRateValue = %v, want %v", got, tc.want)
			}
			// The usage line says there is a rate exactly when there is one.
			line := formatTokenLine(10, 0, 0, tc.received, tc.elapsed)
			if shown := strings.Contains(line, "t/s"); shown != (got != 0) {
				t.Errorf("value = %v but the line %q %s a rate", got, line,
					map[bool]string{true: "shows", false: "omits"}[shown])
			}
		})
	}
}

// Zero has to mean "no rate", or the transcript's omitempty would quietly drop
// a real measurement. The floor is what guarantees it: both operands are
// positive past it, so their quotient cannot round to zero.
func TestARealRateIsNeverZero(t *testing.T) {
	// One token in a second is the slowest thing the floor admits by a wide
	// margin, and it still rounds clear of zero.
	if got := tokenRateValue(1, time.Second); got == 0 {
		t.Errorf("a real measurement rounded to the value that means 'none': %v", got)
	}
	// And the smallest duration past the floor, with one token: 20 t/s.
	if got := tokenRateValue(1, 50*time.Millisecond); got != 20 {
		t.Errorf("tokenRateValue(1, 50ms) = %v, want 20", got)
	}
}

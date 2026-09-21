package main

import (
	"strings"
	"testing"
	"time"
)

// A retention policy is written in days and weeks. time.ParseDuration stops at
// hours, so those are added — and everything it does take still works.
func TestParseAge(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"90d", 90 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"6w", 6 * 7 * 24 * time.Hour},
		{"720h", 720 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"0.5d", 12 * time.Hour},
		{"  30d  ", 30 * 24 * time.Hour},
	} {
		got, err := parseAge(tc.in)
		if err != nil {
			t.Errorf("parseAge(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseAge(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// An age that does not parse must be an error rather than a zero, because a
// zero would mean "strip everything" — the exact default this command is
// shaped to avoid.
func TestParseAgeRefusesWhatWouldStripEverything(t *testing.T) {
	for _, bad := range []string{"", "   ", "0", "0d", "-1d", "-5h", "soon", "90days", "d", "90x", "90"} {
		got, err := parseAge(bad)
		if err == nil {
			t.Errorf("parseAge(%q) = %v, want an error", bad, got)
		}
	}
}

// The bare command reaches for what is plainly old rather than for everything.
func TestTheDefaultAgeIsNotEverything(t *testing.T) {
	if defaultStripAge <= 0 {
		t.Fatalf("the default age is %v, which would strip every payload", defaultStripAge)
	}
	if defaultStripAge != 90*24*time.Hour {
		t.Errorf("the default age is %v, want 90 days", defaultStripAge)
	}
}

// "older than 90 days ago" reads as a date; the span rendering is a length.
func TestHumanSpanReadsAsALength(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{90 * 24 * time.Hour, "90 days"},
		{7 * 24 * time.Hour, "1 week"},
		{14 * 24 * time.Hour, "2 weeks"},
		{24 * time.Hour, "1 day"},
		{36 * time.Hour, "1 day"},
		{2 * time.Hour, "2 hours"},
		{90 * time.Minute, "1 hour"},
	} {
		if got := humanSpan(tc.in); got != tc.want {
			t.Errorf("humanSpan(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// And never the "ago" that the sibling rendering bakes in.
	if strings.Contains(humanSpan(90*24*time.Hour), "ago") {
		t.Error("humanSpan renders a date, not a span")
	}
}

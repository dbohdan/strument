package history

import (
	"strings"
	"testing"
	"time"
)

// The labels are the point of the report: a window described as "this month"
// beside a provider's calendar invoice reads as a discrepancy rather than as
// a different question, so the headers have to say the window they mean.
func TestUsageHeadersNameTheirWindows(t *testing.T) {
	want := []string{"Last 24 hours", "Last 7 days", "Last 30 days"}
	if len(UsageWindows) != len(want) {
		t.Fatalf("%d windows, want %d", len(UsageWindows), len(want))
	}
	for i, w := range UsageWindows {
		if w.Header != want[i] {
			t.Errorf("window %d header = %q, want %q", i, w.Header, want[i])
		}
	}
}

func TestFormatUsageShowsTheThreeWindowsAndFootnotes(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	stamp := func(hoursAgo float64) string {
		return now.Add(-time.Duration(hoursAgo * float64(time.Hour))).UTC().Format(time.RFC3339)
	}
	priced := 0.02
	est := 0.05
	rows := []CostEntry{
		{Time: stamp(1), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 1500, TokensRecv: 120, Cost: &priced},
		{Time: stamp(48), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 900, TokensRecv: 80},
		{Time: stamp(20 * 24), Model: "openrouter/anthropic/claude-haiku-4.5", TokensSent: 2000, TokensRecv: 300, Cost: &est, Estimated: true},
	}

	got := FormatUsage("openrouter", rows, now)

	for _, want := range []string{
		`Usage for provider "openrouter", as of 2026-09-21 12:00 UTC`,
		"Last 24 hours:", "Last 7 days:", "Last 30 days:",
		"openrouter/xiaomi/mimo-v2.5",
		"openrouter/anthropic/claude-haiku-4.5",
		// Thousands render compactly, the way the closing usage line does.
		"1.5k in", "120 out",
		// The unpriced turn is said, not folded into the cost.
		"1 turn reported no cost",
		// The estimated turn is marked, not hidden.
		"1 turn was estimated locally",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not contain %q:\n%s", want, got)
		}
	}

	// The footnotes belong to the window they qualify, not to the report's
	// bottom: the 48-hour-old unpriced row is in the week and month windows
	// but not in the day's.
	dayAt := strings.Index(got, "Last 24 hours:")
	weekAt := strings.Index(got, "Last 7 days:")
	if dayAt < 0 || weekAt < 0 {
		t.Fatalf("report is missing a window header:\n%s", got)
	}
	day := got[dayAt:weekAt]
	week := got[weekAt:]
	if strings.Contains(day, "reported no cost") {
		t.Errorf("the day window claims an unpriced turn it does not hold:\n%s", day)
	}
	if !strings.Contains(week, "reported no cost") {
		t.Errorf("the week window does not footnote its unpriced turn:\n%s", week)
	}
}

// A provider with nothing recorded prints windows that say so, rather than
// three empty frames that read as a broken report.
func TestFormatUsageEmpty(t *testing.T) {
	got := FormatUsage("brand-new", nil, time.Now())
	if !strings.Contains(got, "No usage recorded") {
		t.Errorf("empty report does not say so:\n%s", got)
	}
	for _, want := range []string{"Last 24 hours:", "Last 7 days:", "Last 30 days:"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty report omits the %s window:\n%s", want, got)
		}
	}
}

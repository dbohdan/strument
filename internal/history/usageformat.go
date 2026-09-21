package history

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/render"
)

// Rendering the usage reports, here rather than in the two callers, so the
// terminal command and the REPL command cannot drift: one renderer, two
// surfaces, the same property ViewContext gives "what is in this conversation".

// UsageWindows are the three windows every usage report answers, in the order
// they print. Rolling, on purpose: "last 24 hours" is the same instant
// everywhere and the three windows nest, where calendar days would depend on
// the host's zone and could overlap. The raw rows keep their timestamps, so a
// calendar question remains answerable from the ledger even though the report
// does not ask it.
var UsageWindows = []struct {
	Name   string
	Header string
	Window time.Duration
}{
	{Name: "day", Header: "Last 24 hours", Window: 24 * time.Hour},
	{Name: "week", Header: "Last 7 days", Window: 7 * 24 * time.Hour},
	{Name: "month", Header: "Last 30 days", Window: 30 * 24 * time.Hour},
}

// FormatUsage renders one provider's three windows.
//
// now is a parameter, not a captured time: the caller stamps the report
// "as of" it, and a caller that has a real clock and one under test should
// not be reading different functions. Estimated and unpriced turns are
// footnoted, never silently folded into the totals — a missing number is
// said rather than taken for zero, which is the rule the closing usage line
// already holds.
func FormatUsage(provider string, rows []CostEntry, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage for provider %q, as of %s:\n", provider, now.Format("2006-01-02 15:04 MST"))

	any := false
	for _, w := range UsageWindows {
		rep := AggregateUsage(rows, now, w.Window)
		if rep.Total.Turns > 0 {
			any = true
		}
		fmt.Fprintf(&b, "\n%s:\n%s", w.Header, formatWindow(rep))
	}
	if !any {
		fmt.Fprintf(&b, "\nNo usage recorded in the last 30 days. Usage tracking begins when the version that writes it runs.\n")
	}
	return b.String()
}

// formatWindow renders one window: per-model rows sorted by name, then the
// total, then the footnotes the totals would otherwise be lying by omitting.
func formatWindow(rep UsageReport) string {
	var b strings.Builder
	if rep.Total.Turns == 0 {
		b.WriteString("  No turns in this window.\n")
		return b.String()
	}

	for _, model := range slices.Sorted(maps.Keys(rep.PerModel)) {
		t := rep.PerModel[model]
		fmt.Fprintf(&b, "  %-40s %10s in %10s out\n", model, tokens(t.TokensSent), tokens(t.TokensRecv))
	}
	t := rep.Total
	if t.Cost > 0 {
		fmt.Fprintf(&b, "  %-40s %10s in %10s out   $%.4f total\n",
			"total", tokens(t.TokensSent), tokens(t.TokensRecv), t.Cost)
	} else {
		fmt.Fprintf(&b, "  %-40s %10s in %10s out\n",
			"total", tokens(t.TokensSent), tokens(t.TokensRecv))
	}

	// The footnotes are the honesty of the report. Each says what the number
	// above it does not include, rather than making the reader find out.
	if t.UnpricedTurns > 0 {
		fmt.Fprintf(&b, "  (%s reported no cost, not counted in the totals above.)\n",
			render.Plural(t.UnpricedTurns, "turn", "turns"))
	}
	if t.EstimatedTurns > 0 {
		fmt.Fprintf(&b, "  (%s estimated locally, not by the provider.)\n",
			render.Plural(t.EstimatedTurns, "turn was", "turns were"))
	}
	return b.String()
}

// tokens renders a token count the way the closing usage line does.
func tokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000.0)
}

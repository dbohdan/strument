package history

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
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

	seen := false
	for _, w := range UsageWindows {
		rep := AggregateUsage(rows, now, w.Window)
		if rep.Total.Turns > 0 {
			seen = true
		}
		fmt.Fprintf(&b, "\n%s:\n%s", w.Header, formatWindow(rep))
	}
	if !seen {
		fmt.Fprintf(&b, "\nNo usage recorded in the last 30 days. Usage tracking begins when the version that writes it runs.\n")
	}
	return b.String()
}

// formatWindow renders one window: per-model rows sorted by name, then the
// total, then the footnotes the totals would otherwise be lying by omitting.
// The table goes through a tabwriter, so the name column sizes itself to the
// rows instead of to a fixed 35-char format: short model names no longer
// leave a wide gutter, and long ones no longer push the figures right. The
// figures keep their columns because tokens() renders every count at a fixed
// 6-char width — suffix included — which is what makes tabwriter's
// writer-wide left alignment enough. Only a count that rounds past that
// width (999.95k and up, 1e9 and up) can still shift its word.
func formatWindow(rep UsageReport) string {
	if rep.Total.Turns == 0 {
		return "  No turns in this window.\n"
	}

	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)

	for _, model := range slices.Sorted(maps.Keys(rep.PerModel)) {
		t := rep.PerModel[model]
		// The first two cells are tab-terminated and so aligned; the last
		// cell of each row has no trailing tab, which keeps the table from
		// gaining a phantom empty column at the right.
		fmt.Fprintf(w, "  %s\t%s in\t%s out\n",
			model, tokens(t.TokensSent), tokens(t.TokensRecv))
	}
	t := rep.Total
	if t.Cost > 0 {
		fmt.Fprintf(w, "  total\t%s in\t%s out\t$%.2f total\n",
			tokens(t.TokensSent), tokens(t.TokensRecv), t.Cost)
	} else {
		fmt.Fprintf(w, "  total\t%s in\t%s out\n",
			tokens(t.TokensSent), tokens(t.TokensRecv))
	}
	// Flush before writing the footnotes to b: the writer buffers rows until
	// it knows the column widths, so anything written to b ahead of Flush
	// would come out before the table.
	_ = w.Flush()

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

// tokens renders a token count the way the closing usage line does
// but with a fixed width and support for millions.
func tokens(n int) string {
	if n < 1_000 {
		return fmt.Sprintf("%6d", n)
	}

	if n < 1_000_000 {
		return fmt.Sprintf("%5.1fk", float64(n)/1_000.0)
	}

	return fmt.Sprintf("%5.1fm", float64(n)/1_000_000.0)
}

package history

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAppendUsageIsOneLinePerTurn(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	cost := 0.0125
	if err := AppendUsage("openrouter", CostEntry{Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 10, TokensRecv: 2, Cost: &cost}); err != nil {
		t.Fatal(err)
	}
	// No cost reported: the field is absent rather than a misleading zero,
	// matching the cost ledger's row shape.
	if err := AppendUsage("openrouter", CostEntry{Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 20, TokensRecv: 3}); err != nil {
		t.Fatal(err)
	}

	p, err := UsagePath("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(data)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), data)
	}
	if strings.Contains(string(lines[1]), `"cost"`) {
		t.Errorf("an unpriced turn should omit cost: %s", lines[1])
	}

	var first CostEntry
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("line 1 is not valid JSON: %v", err)
	}
	if first.Cost == nil || *first.Cost != cost || first.TokensSent != 10 {
		t.Errorf("round trip lost data: %+v", first)
	}
	if first.Time == "" {
		t.Error("time not stamped")
	}
	if filepath.Dir(p) != filepath.Join(os.Getenv("XDG_STATE_HOME"), "strument", "usage") {
		t.Errorf("usage path = %s, want it under the state dir beside projects/", p)
	}
}

// A provider's rows belong to that provider and to no other: one file each,
// keyed by the name in the config.
func TestUsageFilesArePerProvider(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := AppendUsage("openrouter", CostEntry{Model: "a/b", TokensSent: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendUsage("local", CostEntry{Model: "c/d", TokensSent: 2}); err != nil {
		t.Fatal(err)
	}

	got, err := ReadUsage("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Model != "a/b" {
		t.Errorf("openrouter rows = %+v, want only a/b's", got)
	}
}

// A provider that has never been used has no file, which is an answer rather
// than an error — `strument usage brand-new-provider` should print an empty
// report, not fail.
func TestReadUsageWithoutAFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	rows, err := ReadUsage("never-used")
	if err != nil {
		t.Fatalf("reading an absent ledger should not fail: %v", err)
	}
	if rows != nil {
		t.Errorf("rows = %+v, want none", rows)
	}
}

func TestUsageProvidersListsEveryFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if got, err := UsageProviders(); err != nil || got != nil {
		t.Errorf("with no usage dir, providers = %v, %v; want nil, nil", got, err)
	}
	for _, name := range []string{"local", "openrouter"} {
		if err := AppendUsage(name, CostEntry{Model: "a/b", TokensSent: 1}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := UsageProviders()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"local", "openrouter"}; !slices.Equal(got, want) {
		t.Errorf("providers = %v, want %v", got, want)
	}
}

// The name arrives from user config and becomes a filename, so the rules that
// protect a session name from being a path protect it too.
func TestUsageNameRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "..", "a/b", ".hidden", "sp ace"} {
		if _, err := UsageName(name); err == nil {
			t.Errorf("UsageName(%q) accepted an unusable name", name)
		}
	}
	for _, name := range []string{"openrouter", "local-gw", "my.proxy.v2"} {
		if _, err := UsageName(name); err != nil {
			t.Errorf("UsageName(%q) refused a usable name: %v", name, err)
		}
	}
}

// One damaged line costs itself, not the file: the turns around it stay
// reportable, because a ledger that never rewrites itself must also be read
// without rewriting itself.
func TestReadUsageSkipsADamagedLine(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := AppendUsage("p", CostEntry{Model: "a/b", TokensSent: 1}); err != nil {
		t.Fatal(err)
	}
	p, _ := UsagePath("p")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"time\": not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := AppendUsage("p", CostEntry{Model: "a/b", TokensSent: 2}); err != nil {
		t.Fatal(err)
	}

	rows, err := ReadUsage("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].TokensSent != 1 || rows[1].TokensSent != 2 {
		t.Errorf("rows = %+v, want the two good turns and not the damaged one", rows)
	}
}

func TestAggregateUsageSumsWithinTheWindow(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	stamp := func(hoursAgo float64) string {
		return now.Add(-time.Duration(hoursAgo * float64(time.Hour))).UTC().Format(time.RFC3339)
	}
	cost := func(v float64) *float64 { return &v }

	rows := []CostEntry{
		{Time: stamp(1), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 100, TokensRecv: 10, Cost: cost(0.02)},
		// Inside the week, outside the day.
		{Time: stamp(48), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 200, TokensRecv: 20, Cost: cost(0.04)},
		// Inside the month, outside the week. A different model, so the
		// per-model rows have something to separate.
		{Time: stamp(24 * 20), Model: "anthropic/claude-haiku-4.5", TokensSent: 400, TokensRecv: 40, Cost: cost(0.20)},
		// Older than every window.
		{Time: stamp(24 * 60), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 800, TokensRecv: 80, Cost: cost(0.99)},
		// Exactly on the 7-day cutoff: strictly-newer means it falls out. One
		// arbitrary choice, stated in AggregateUsage and pinned here.
		{Time: stamp(24 * 7), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 1600, TokensRecv: 160, Cost: cost(1.50)},
		// Unreadable instant: belongs to no window rather than to whichever
		// one a guess would favor.
		{Time: "not a time", Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 3200, TokensRecv: 320, Cost: cost(9.99)},
		// Cache tokens are part of the sent/received totals, not beside them.
		{Time: stamp(2), Model: "openrouter/xiaomi/mimo-v2.5", TokensSent: 50, TokensRecv: 5, CacheRead: 30, CacheWrite: 8, Cost: cost(0.01)},
		// An error row: no tokens, no cost. Counts as a turn nowhere.
		{Time: stamp(3), Model: "openrouter/xiaomi/mimo-v2.5"},
	}

	day := AggregateUsage(rows, now, 24*time.Hour)
	if got, want := day.Total.TokensSent, 150; got != want {
		t.Errorf("day tokens sent = %d, want %d", got, want)
	}
	if math.Abs(day.Total.Cost-0.03) > 1e-9 {
		t.Errorf("day cost = %v, want 0.03", day.Total.Cost)
	}
	if day.Total.CacheRead != 30 || day.Total.CacheWrite != 8 {
		t.Errorf("day cache = %d/%d, want 30/8", day.Total.CacheRead, day.Total.CacheWrite)
	}
	if len(day.PerModel) != 1 {
		t.Errorf("day models = %v, want one", day.PerModel)
	}

	week := AggregateUsage(rows, now, 7*24*time.Hour)
	if got, want := week.Total.TokensSent, 350; got != want {
		t.Errorf("week tokens sent = %d, want %d", got, want)
	}
	// Float sums: compare to the cent, which is the precision a cost total
	// is read at, not to the last bit of a binary double.
	if math.Abs(week.Total.Cost-0.07) > 1e-9 {
		t.Errorf("week cost = %v, want 0.07", week.Total.Cost)
	}

	month := AggregateUsage(rows, now, 30*24*time.Hour)
	// The 7-days-ago row sits inside the month window too — only the
	// 60-days-ago and unparseable rows fall out.
	if got, want := month.Total.TokensSent, 2350; got != want {
		t.Errorf("month tokens sent = %d, want %d", got, want)
	}
	haiku := month.PerModel["anthropic/claude-haiku-4.5"]
	if haiku.TokensSent != 400 || math.Abs(haiku.Cost-0.20) > 1e-9 {
		t.Errorf("month haiku = %+v, want its own row inside the month", haiku)
	}

	// The windows are nested, so the totals have to be monotonic whatever the
	// rows — this is the property that makes the output sanity-checkable.
	if day.Total.TokensSent > week.Total.TokensSent || week.Total.TokensSent > month.Total.TokensSent {
		t.Errorf("windows not nested: day %d, week %d, month %d",
			day.Total.TokensSent, week.Total.TokensSent, month.Total.TokensSent)
	}
	if len(month.PerModel) != 2 {
		t.Errorf("month models = %v, want two", month.PerModel)
	}
}

// An unpriced turn sums its tokens and is counted separately, never as $0:
// the aggregate holds to the same rule as the closing usage line, that a
// missing number is said rather than silently taken for zero.
func TestAggregateUsageCountsUnpricedAndEstimated(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	priced := 0.05
	rows := []CostEntry{
		{Time: stamp, Model: "a/b", TokensSent: 10, TokensRecv: 1, Cost: &priced},
		{Time: stamp, Model: "a/b", TokensSent: 20, TokensRecv: 2},
		{Time: stamp, Model: "a/b", TokensSent: 40, TokensRecv: 4, Cost: &priced, Estimated: true},
		{Time: stamp, Model: "a/b"},
	}

	got := AggregateUsage(rows, now, 24*time.Hour)
	if got.Total.TokensSent != 70 {
		t.Errorf("tokens sent = %d, want all three token-bearing rows", got.Total.TokensSent)
	}
	if got.Total.Cost != 0.10 {
		t.Errorf("cost = %v, want only the priced rows", got.Total.Cost)
	}
	if got.Total.UnpricedTurns != 1 {
		t.Errorf("unpriced = %d, want 1", got.Total.UnpricedTurns)
	}
	if got.Total.EstimatedTurns != 1 {
		t.Errorf("estimated = %d, want 1", got.Total.EstimatedTurns)
	}
	if got.Total.Turns != 4 {
		t.Errorf("turns = %d, want 4 — an error row is still a turn", got.Total.Turns)
	}
}

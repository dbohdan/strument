package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Per-provider usage tracking, beside the per-project cost ledger.
//
// cost.jsonl answers "what has this project cost me"; a provider file answers
// "what is this provider costing me", which is a question across projects and
// so does not belong to any one of them. The two ledgers carry the same row —
// a usage row is the turn accounting a turn already records, minus the fields
// that only make sense per project (session, steps, files) — and CostEntry is
// reused rather than a second type declared, so the two cannot drift.
//
// Not a project artifact: the files live beside projects/ rather than inside
// one, which is why the artifact table in artifact.go does not name them. The
// table exists so `strument project adopt` merges every file a project
// directory can hold; nothing here is keyed by project path, so there is
// nothing to merge, and the guard test that walks a project directory is
// unaffected.

// UsageAll is the name `strument usage all` treats as "every provider".
//
// It cannot be a provider name: ValidSessionName's rule (shared by
// UsageName) allows "all", so a config really could name a provider "all" —
// and that collision is resolved toward the aggregate, with the rename
// suggested as the way out.
const UsageAll = "all"

// UsagePath is the per-provider usage ledger for a provider name.
//
// The name comes from user config (provider(..., name=...)), and it becomes a
// filename, so it is validated rather than trusted: the same rule session
// names follow, because the answer to "what may a name contain" is a property
// of a filesystem, not of what the name is for.
func UsagePath(provider string) (string, error) {
	name, err := UsageName(provider)
	if err != nil {
		return "", err
	}
	base, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "usage", name+".jsonl"), nil
}

// UsageName validates a provider name for use as a usage file. The error is
// written to be shown to whoever wrote the config, so it says what is allowed.
func UsageName(provider string) (string, error) {
	if err := ValidSessionName(provider); err != nil {
		return "", fmt.Errorf("%q is not a usable provider name: start with a letter "+
			"or digit, then letters, digits, dots, dashes and underscores", provider)
	}
	return provider, nil
}

// AppendUsage adds one turn to a provider's ledger.
//
// Appended, never rewritten, one line per turn: the same shape cost.jsonl has,
// for the same reasons — a partial write costs at most the last line, and two
// concurrent sessions cannot race over a read-modify-write of a daily bucket.
func AppendUsage(provider string, e CostEntry) error {
	p, err := UsagePath(provider)
	if err != nil {
		return err
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ReadUsage reads a provider's ledger. A provider with no file has no usage,
// which is an answer rather than an error.
func ReadUsage(provider string) ([]CostEntry, error) {
	p, err := UsagePath(provider)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []CostEntry
	for _, line := range splitLines(data) {
		var e CostEntry
		if err := json.Unmarshal(line, &e); err != nil {
			// One damaged line is skipped rather than fatal, the way a ledger
			// that never rewrites itself must be read: the turns around it
			// stay reportable, and the damaged line is one turn's accounting,
			// not the file's.
			continue
		}
		rows = append(rows, e)
	}
	return rows, nil
}

// UsageProviders lists every provider that has a usage file, sorted.
func UsageProviders() ([]string, error) {
	base, err := stateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(base, "usage"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ext := filepath.Ext(name); ext != ".jsonl" {
			continue
		}
		out = append(out, name[:len(name)-len(".jsonl")])
	}
	slices.Sort(out)
	return out, nil
}

// UsageTotals is one rolling window's aggregate, summed across models.
type UsageTotals struct {
	// TokensSent and TokensRecv sum every row in the window, priced or not.
	TokensSent int
	TokensRecv int
	// CacheRead and CacheWrite are the cache-attributed halves of sent and
	// received; they are part of the sent/received totals, not beside them.
	CacheRead  int
	CacheWrite int
	// Cost sums the rows that reported one. A provider reports nothing on some
	// turns (a free model, an error row, a stream that never sent usage), and
	// summing those as zero would present a made-up total — "never fabricate
	// cost" applies to an aggregate as much as to a turn. UnpricedTurns is how
	// the honest answer says itself.
	Cost float64
	// UnpricedTurns counts rows that reported tokens but no cost. A row with
	// no tokens at all is an error row, not a bill, and counts nowhere.
	UnpricedTurns int
	// EstimatedTurns counts rows whose cost was a local estimate rather than
	// the provider's number. Estimated cost is marked, not hidden: the total
	// is still the best number there is, but a reader deserves to know which
	// part of it is the provider's own accounting.
	EstimatedTurns int
	Turns          int
}

// addRow folds one row into the totals.
func (t *UsageTotals) addRow(e CostEntry) {
	t.TokensSent += e.TokensSent
	t.TokensRecv += e.TokensRecv
	t.CacheRead += e.CacheRead
	t.CacheWrite += e.CacheWrite
	t.Turns++
	if e.TokensSent == 0 && e.TokensRecv == 0 {
		return
	}
	if e.Cost != nil {
		t.Cost += *e.Cost
		if e.Estimated {
			t.EstimatedTurns++
		}
	} else {
		t.UnpricedTurns++
	}
}

// UsageReport is one window's breakdown, per model and in total.
type UsageReport struct {
	PerModel map[string]UsageTotals
	Total    UsageTotals
}

// AggregateUsage sums the rows within the window ending at now: a row counts
// when its timestamp is strictly newer than now-window. Window edges are
// rolling — last 24 hours, 7 days, 30 days — precisely so no calendar or
// timezone question reaches the sum; the timestamps stay in the rows, so a
// calendar question remains answerable from the raw ledger.
//
// A row whose timestamp does not parse is skipped: an unreadable instant
// belongs to no window, and guessing one would put the row in whichever
// window the guess favored. Rows are not required to be in order — they are
// appended in turn order, but an adopted project's ledger can interleave —
// and the sum must not care.
func AggregateUsage(rows []CostEntry, now time.Time, window time.Duration) UsageReport {
	rep := UsageReport{PerModel: map[string]UsageTotals{}}
	cutoff := now.Add(-window)
	for _, e := range rows {
		ts, err := time.Parse(time.RFC3339, e.Time)
		if err != nil || !ts.After(cutoff) {
			continue
		}
		m := rep.PerModel[e.Model]
		m.addRow(e)
		rep.PerModel[e.Model] = m
		rep.Total.addRow(e)
	}
	return rep
}

// splitLines splits JSON Lines, tolerating a missing final newline and
// skipping blank lines.
func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

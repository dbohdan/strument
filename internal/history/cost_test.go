package history

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestAppendCostIsOneLinePerTurn(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	cost := 0.0125
	if err := AppendCost(project, CostEntry{Model: "a/b", TokensSent: 10, TokensRecv: 2, Cost: &cost, Steps: 1}); err != nil {
		t.Fatal(err)
	}
	// No cost reported: the field is absent rather than a misleading zero.
	if err := AppendCost(project, CostEntry{Model: "a/b", TokensSent: 20, TokensRecv: 3, Steps: 4, FilesChanged: 2}); err != nil {
		t.Fatal(err)
	}

	p, _ := CostPath(project)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), data)
	}
	if strings.Contains(lines[1], `"cost"`) {
		t.Errorf("an unpriced turn should omit cost: %s", lines[1])
	}

	var first CostEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 is not valid JSON: %v", err)
	}
	if first.Cost == nil || *first.Cost != cost || first.TokensSent != 10 {
		t.Errorf("round trip lost data: %+v", first)
	}
	if first.Time == "" {
		t.Error("time not stamped")
	}

	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission bits")
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Errorf("cost.jsonl mode = %04o, want %04o", perm, fileMode)
	}
}

// A project holds several sessions and one ledger, so the row has to say which
// conversation it belongs to — and a row from before sessions existed has to
// stay distinguishable from one that named a session, which is why the field
// is omitted rather than defaulted.
func TestCostRowsNameTheirSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	if err := AppendCost(project, CostEntry{Session: "review", Model: "a/b", Steps: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendCost(project, CostEntry{Model: "a/b", Steps: 1}); err != nil {
		t.Fatal(err)
	}

	p, _ := CostPath(project)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), data)
	}

	var named CostEntry
	if err := json.Unmarshal([]byte(lines[0]), &named); err != nil {
		t.Fatal(err)
	}
	if named.Session != "review" {
		t.Errorf("session = %q, want the session the turn ran in", named.Session)
	}
	if strings.Contains(lines[1], `"session"`) {
		t.Errorf("a row with no session should omit the field: %s", lines[1])
	}
}

package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeState fills a state directory with the files a merge acts on, so a test
// can build "the orphan" and "the project as it is now" without a session.
func writeState(t *testing.T, dir string, transcript, cost, resumeUpdated string) {
	t.Helper()
	if err := os.MkdirAll(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if body == "" {
			return
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), fileMode); err != nil {
			t.Fatal(err)
		}
	}
	write(artifacts[artTranscript].name, transcript)
	write(artifacts[artCost].name, cost)
	if resumeUpdated != "" {
		write(artifacts[artResume].name, mustJSON(t, Resume{
			Version: resumeVersion, Updated: resumeUpdated, Model: resumeUpdated,
		})+"\n")
	}
}

func costRow(t *testing.T, when, model string) string {
	t.Helper()
	return mustJSON(t, CostEntry{Time: when, Model: model, Steps: 1}) + "\n"
}

// mustJSON keeps the fixtures readable: a marshal that cannot fail for these
// types still has to have its error checked, and doing it inline four times
// buries the fixture in error handling.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAdoptMergesAppendOnlyRecords is the ordinary case: a project renamed,
// nothing yet written at the new path.
func TestAdoptMergesAppendOnlyRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	orphan := filepath.Join(t.TempDir(), "orphan")
	writeState(t, orphan, "# older session\n", costRow(t, "2026-09-01T10:00:00Z", "old"), "2026-09-01T10:00:00Z")

	if err := Adopt(project, orphan, "witness"); err != nil {
		t.Fatal(err)
	}
	dst, err := ProjectDir(project)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dst, artifacts[artTranscript].name))
	if !strings.Contains(got, "older session") {
		t.Errorf("transcript = %q, want the adopted session's turns", got)
	}
	// The source must survive: merging appends, so an unwanted adopt is only
	// recoverable from the copy.
	aside, err := filepath.Glob(orphan + ".adopted-*")
	if err != nil || len(aside) != 1 {
		t.Fatalf("source directories matching .adopted-*: %v (err %v), want exactly one", aside, err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Error("the source directory is still at its old name; it must be moved, not copied")
	}
}

// TestAdoptOverExistingData is the case that decided the merge policy: the user
// renames the project, does not notice the history is gone, has a session at the
// new path, and only then adopts.
//
// Both halves must survive for the append-only records, and the newest must win
// for the single-valued ones.
func TestAdoptOverExistingData(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	// The accidental session at the new path.
	dst, err := EnsureProjectDir(project, "witness")
	if err != nil {
		t.Fatal(err)
	}
	writeState(t, dst, "# accidental session\n", costRow(t, "2026-09-05T10:00:00Z", "new"), "2026-09-05T10:00:00Z")

	orphan := filepath.Join(t.TempDir(), "orphan")
	writeState(t, orphan, "# older session\n", costRow(t, "2026-09-01T10:00:00Z", "old"), "2026-09-01T10:00:00Z")

	if err := Adopt(project, orphan, "witness"); err != nil {
		t.Fatal(err)
	}

	transcript := readFile(t, filepath.Join(dst, artifacts[artTranscript].name))
	for _, want := range []string{"older session", "accidental session"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("transcript lost %q:\n%s", want, transcript)
		}
	}
	if strings.Index(transcript, "older") > strings.Index(transcript, "accidental") {
		t.Error("the adopted turns must come first: they happened first")
	}

	rows := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dst, artifacts[artCost].name))), "\n")
	if len(rows) != 2 {
		t.Fatalf("cost rows = %d, want both ledgers' rows", len(rows))
	}
	if !strings.Contains(rows[0], `"old"`) {
		t.Errorf("cost rows out of time order:\n%s", strings.Join(rows, "\n"))
	}

	// resume.json is one value, and the accidental session's is the newer one.
	var res Resume
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dst, artifacts[artResume].name))), &res); err != nil {
		t.Fatal(err)
	}
	if res.Updated != "2026-09-05T10:00:00Z" {
		t.Errorf("resume Updated = %q, want the newer of the two", res.Updated)
	}
}

// TestAdoptSortsAJSONLedgerBothWays covers the direction that is easy to get
// wrong by assuming an orphan is always the older one. Adopting can go either
// way — a project moved *back* to a path it once had, say — so the ledger is
// sorted rather than concatenated.
func TestAdoptSortsAJSONLedgerBothWays(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	dst, err := EnsureProjectDir(project, "witness")
	if err != nil {
		t.Fatal(err)
	}
	// The destination holds the *older* rows this time.
	writeState(t, dst, "", costRow(t, "2026-09-01T10:00:00Z", "first"), "")
	orphan := filepath.Join(t.TempDir(), "orphan")
	writeState(t, orphan, "", costRow(t, "2026-09-05T10:00:00Z", "second"), "")

	if err := Adopt(project, orphan, "witness"); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dst, artifacts[artCost].name))), "\n")
	if len(rows) != 2 {
		t.Fatalf("cost rows = %d, want 2", len(rows))
	}
	if !strings.Contains(rows[0], `"first"`) || !strings.Contains(rows[1], `"second"`) {
		t.Errorf("rows are not in time order when the orphan is newer:\n%s", strings.Join(rows, "\n"))
	}
}

// TestMatchesNeedsBothHalves pins the match rule. The witness alone is not
// enough — every clone of a repository shares one — and neither is the path
// being gone.
func TestMatchesNeedsBothHalves(t *testing.T) {
	cases := []struct {
		name    string
		c       Candidate
		witness string
		want    bool
	}{
		{"orphan with a matching witness", Candidate{Root: Root{GitRootCommit: "a"}, Exists: false}, "a", true},
		{"the project is still there", Candidate{Root: Root{GitRootCommit: "a"}, Exists: true}, "a", false},
		{"a different repository", Candidate{Root: Root{GitRootCommit: "b"}, Exists: false}, "a", false},
		{"the candidate has no witness", Candidate{Root: Root{}, Exists: false}, "a", false},
		{"we have no witness", Candidate{Root: Root{GitRootCommit: "a"}, Exists: false}, "", false},
		// Two projects with no repository must not match each other just by
		// both being empty, which is what a naive equality would do.
		{"neither has a witness", Candidate{Root: Root{}, Exists: false}, "", false},
	}
	for _, tc := range cases {
		if got := tc.c.Matches(tc.witness); got != tc.want {
			t.Errorf("%s: Matches(%q) = %v, want %v", tc.name, tc.witness, got, tc.want)
		}
	}
}

// TestFindOrphanRefusesTwoMatches is the two-clones case. Tier 0's evidence
// cannot tell two checkouts of one repository apart, so when two of them are
// both gone it must decline rather than pick.
func TestFindOrphanRefusesTwoMatches(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	projects := filepath.Join(state, "strument", "projects")

	for _, name := range []string{"clone-a-1111", "clone-b-2222"} {
		dir := filepath.Join(projects, name)
		if err := os.MkdirAll(dir, dirMode); err != nil {
			t.Fatal(err)
		}
		body := mustJSON(t, Root{Path: filepath.Join(state, "gone", name), GitRootCommit: "shared"})
		if err := os.WriteFile(filepath.Join(dir, "root"), []byte(body+"\n"), fileMode); err != nil {
			t.Fatal(err)
		}
	}

	// One alone is found, so the nil below means "two matched", not "the
	// fixture never worked".
	all, err := Candidates()
	if err != nil {
		t.Fatal(err)
	}
	matching := 0
	for _, c := range all {
		if c.Matches("shared") {
			matching++
		}
	}
	if matching != 2 {
		t.Fatalf("the fixture must produce two matching orphans, got %d", matching)
	}

	got, ok, err := FindOrphan("shared")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("FindOrphan picked %s; with two clones gone it must decline", got.Dir)
	}
}

// TestCandidatesSkipsAdoptedDirectories keeps a preserved source from coming
// back as a candidate — it would otherwise be offered again on the next run,
// since its recorded path is exactly the one that no longer exists.
func TestCandidatesSkipsAdoptedDirectories(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "strument", "projects", "proj-1234.adopted-20260906T101010Z")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, Root{Path: filepath.Join(state, "gone"), GitRootCommit: "w"})
	if err := os.WriteFile(filepath.Join(dir, "root"), []byte(body+"\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	all, err := Candidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("candidates = %v, want none: an already-adopted directory must not be re-offered", all)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

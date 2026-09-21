package history

import (
	"os"
	"strings"
	"testing"
	"time"
)

// stripFixture is a project with one session whose segment references blobs,
// aged by setting the segment's modification time.
func stripFixture(t *testing.T, project, session string, age time.Duration, payloads ...string) []string {
	t.Helper()
	if _, err := EnsureSessionDir(project, session); err != nil {
		t.Fatal(err)
	}
	var hashes []string
	var b strings.Builder
	b.WriteString(`{"type":"session","version":1}` + "\n")
	for i, payload := range payloads {
		h, err := PutBlob(project, []byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
		// Alternate between a result's payload and a call's arguments, so the
		// sweep has to look at both places a hash can sit.
		if i%2 == 0 {
			b.WriteString(`{"type":"message","role":"tool","tool_call_id":"c","blob":"` + h + `","bytes":1,"summary":"s"}` + "\n")
		} else {
			b.WriteString(`{"type":"message","role":"assistant","tool_calls":[{"id":"c","name":"write","blob":"` + h + `","bytes":1,"summary":"s"}]}` + "\n")
		}
	}
	seg, err := NewLogSegment(project, session, time.Now().Add(-age))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seg, []byte(b.String()), fileMode); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(seg, when, when); err != nil {
		t.Fatal(err)
	}
	return hashes
}

func newStripProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	return project
}

// The sweep's whole job: old payloads go, recent ones stay, and every record
// stays whatever happens.
func TestStripRemovesOnlyWhatNothingRecentPointsAt(t *testing.T) {
	project := newStripProject(t)
	old := stripFixture(t, project, "archive", 200*24*time.Hour, "an old result", "old arguments")
	recent := stripFixture(t, project, "current", time.Hour, "a recent result")

	plan, err := PlanStrip(project, time.Now().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Remove) != 2 {
		t.Fatalf("plan removes %d payloads, want the 2 old ones: %+v", len(plan.Remove), plan)
	}
	if plan.Keep != 1 {
		t.Errorf("plan keeps %d, want the recent one", plan.Keep)
	}
	if plan.Orphans != 0 {
		t.Errorf("plan found %d orphans, want none", plan.Orphans)
	}

	if _, _, err := ApplyStrip(project, plan); err != nil {
		t.Fatal(err)
	}
	for _, h := range old {
		if _, ok := GetBlob(project, h); ok {
			t.Errorf("an old payload survived the sweep: %s", h)
		}
	}
	if _, ok := GetBlob(project, recent[0]); !ok {
		t.Error("a recent payload was swept")
	}
	// Never a record. The hash, the size and the first line stay behind, which
	// is what keeps a stripped conversation readable and replayable.
	records, missing, err := ReadSessionRecords(project, "archive")
	if err != nil {
		t.Fatal(err)
	}
	if missing != 2 {
		t.Errorf("%d payloads reported missing, want the 2 stripped", missing)
	}
	if len(records) == 0 {
		t.Fatal("the sweep took the records with the payloads")
	}
	for _, r := range records {
		if r.Blob != "" && (r.Summary == "" || r.Bytes == 0) {
			t.Errorf("a stripped record lost what stood in for its payload: %+v", r)
		}
	}
}

// Sharing is what makes one removal take every copy of a secret — and also
// what means a blob cannot go while any recent record still points at it.
func TestAPayloadSharedWithARecentSessionStays(t *testing.T) {
	project := newStripProject(t)
	shared := "the same file, read twice"
	oldHashes := stripFixture(t, project, "archive", 200*24*time.Hour, shared)
	newHashes := stripFixture(t, project, "current", time.Hour, shared)
	if oldHashes[0] != newHashes[0] {
		t.Fatal("the fixture did not actually share a blob")
	}

	plan, err := PlanStrip(project, time.Now().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Empty() {
		t.Errorf("a payload a recent session still points at was swept: %+v", plan)
	}
}

// A blob nothing refers to has no age to compare, so no cutoff can keep it.
func TestOrphanedPayloadsGoWhateverTheCutoff(t *testing.T) {
	project := newStripProject(t)
	stripFixture(t, project, "current", time.Hour, "a referenced result")
	orphan, err := PutBlob(project, []byte("left by a deleted session"))
	if err != nil {
		t.Fatal(err)
	}

	// A cutoff so old that nothing could be older than it.
	plan, err := PlanStrip(project, time.Now().Add(-100*365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Remove) != 1 || plan.Remove[0] != orphan {
		t.Fatalf("plan = %+v, want only the orphan", plan)
	}
	if plan.Orphans != 1 {
		t.Errorf("orphans = %d, want 1", plan.Orphans)
	}
	if plan.Keep != 1 {
		t.Errorf("keep = %d, want the referenced payload", plan.Keep)
	}
}

// Planning is not doing. The command shows the cost and asks first, which only
// works if working it out changes nothing.
func TestPlanningRemovesNothing(t *testing.T) {
	project := newStripProject(t)
	hashes := stripFixture(t, project, "archive", 200*24*time.Hour, "an old result")

	if _, err := PlanStrip(project, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetBlob(project, hashes[0]); !ok {
		t.Error("planning a sweep deleted a payload")
	}
}

// A project with nothing stored is not an error, and neither is one whose
// payloads are all in use.
func TestStripOnAQuietProject(t *testing.T) {
	project := newStripProject(t)
	plan, err := PlanStrip(project, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Empty() || plan.Keep != 0 {
		t.Errorf("an empty project planned %+v", plan)
	}
}

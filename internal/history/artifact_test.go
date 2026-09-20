package history

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"
)

// populateProjectDir drives every writer this package has, so the state
// directory afterwards holds one of everything a real session leaves.
//
// It calls the writers rather than listing file names, which is the whole
// point: a test that enumerates names would pass for a name it was told about,
// and the failure being guarded is someone adding a writer and telling nobody.
func populateProjectDir(t *testing.T, project string) string {
	t.Helper()
	dir, err := EnsureProjectDir(project, "root-commit")
	if err != nil {
		t.Fatal(err)
	}

	p, err := DefaultPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(p).Append(Turn{User: "hi", Assistant: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendCost(project, CostEntry{Time: "2026-09-06T10:00:00Z", Model: "m", Steps: 1}); err != nil {
		t.Fatal(err)
	}
	if err := SaveResume(project, DefaultSession, Resume{Version: resumeVersion, Updated: "2026-09-06T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveUndo(project, DefaultSession, UndoState{Version: undoVersion, Updated: "2026-09-06T10:00:00Z"}, 0); err != nil {
		t.Fatal(err)
	}
	in, err := InputHistoryPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, []byte("a prompt\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	lp, err := LockPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := Dismiss(project, "some-other-project-1234"); err != nil {
		t.Fatal(err)
	}
	if err := SetCurrentSession(project, DefaultSession); err != nil {
		t.Fatal(err)
	}
	lk := flock.New(lp)
	if _, err := lk.TryLock(); err != nil {
		t.Fatal(err)
	}
	if err := lk.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestProjectDirHoldsOnlyRegisteredArtifacts is the guard that makes the merge
// policy table impossible to forget.
//
// A project's state directory can be merged into another one when a renamed
// project is adopted, and the merge is driven by the table in artifact.go. A
// file written by some new writer that nobody registered has no policy, so
// adopting would drop it — silently, and in a way nobody notices until they go
// looking for something a year old.
//
// So: run every writer, then read the directory back and insist that what is
// there is what was declared. The check is on the filesystem rather than on the
// table, because the table cannot be wrong about itself.
func TestProjectDirHoldsOnlyRegisteredArtifacts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	dir := populateProjectDir(t, project)

	known := artifactNames()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if id, ok := known[e.Name()]; ok && artifacts[id].dir != e.IsDir() {
			kind := map[bool]string{true: "a directory", false: "a file"}
			t.Errorf("%q is %s on disk but registered as %s; adopt would apply the wrong policy",
				e.Name(), kind[e.IsDir()], kind[artifacts[id].dir])
		}
		if _, ok := known[e.Name()]; !ok {
			t.Errorf("%q is in a project's state directory but not in artifact.go's table, "+
				"so `strument project adopt` would drop it — register it with a merge policy",
				e.Name())
			continue
		}
		seen[e.Name()] = true
	}
	// The other direction: a registered artifact that no writer produces means
	// the table has drifted ahead of the code, and the test above would then be
	// checking a set nothing fills.
	for name := range known {
		if !seen[name] {
			t.Errorf("%q is registered but no writer in this package produced it; "+
				"either populateProjectDir is out of date or the entry is dead", name)
		}
	}

	// And the same, one level down. A session directory is where resume and
	// undo live now, so a file that skipped sessionArtifacts is one
	// mergeSessions would walk past — the same silent drop, out of reach of
	// the loop above because it only reads the top level.
	sessionDir, err := SessionDir(project, DefaultSession)
	if err != nil {
		t.Fatal(err)
	}
	sessionEntries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	knownSession := sessionArtifactNames()
	seenSession := map[string]bool{}
	for _, e := range sessionEntries {
		if _, ok := knownSession[e.Name()]; !ok {
			t.Errorf("%q is in a session directory but not in sessionArtifacts, "+
				"so adopting a session both projects have would drop it", e.Name())
			continue
		}
		seenSession[e.Name()] = true
	}
	for name := range knownSession {
		if !seenSession[name] {
			t.Errorf("%q is registered as a session artifact but no writer produced it", name)
		}
	}
}

// A directory artifact must carry a directory policy. Every file policy begins with
// os.ReadFile, which returns EISDIR on a directory — and EISDIR is not
// os.ErrNotExist, so adopt.go's missing-source escape hatches do not fire.
// Adopt iterates the table in map order and returns on the first error, so the
// result would be a merge that aborted after rewriting an arbitrary,
// irreproducible subset of the other artifacts.
func TestArtifactKindMatchesItsPolicy(t *testing.T) {
	for _, table := range []map[string]artifact{artifacts, sessionArtifacts} {
		for id, a := range table {
			if a.dir && !dirPolicies[a.policy] {
				t.Errorf("artifact %q (%s) is a directory but its policy reads a file; "+
					"every file policy begins with os.ReadFile, which fails with EISDIR", id, a.name)
			}
			if !a.dir && dirPolicies[a.policy] {
				t.Errorf("artifact %q (%s) has a directory policy but is not a directory", id, a.name)
			}
		}
	}
}

// TestEveryArtifactHasAPolicyReason pins the second half of a registry entry.
// A policy with no stated reason is one that gets changed by guess, and the
// reason is printed in `strument project adopt`'s plan, where a reader has to
// be able to judge it.
func TestEveryArtifactHasAPolicyReason(t *testing.T) {
	for id, a := range artifacts {
		if a.name == "" {
			t.Errorf("artifact %q has no file name", id)
		}
		if a.why == "" {
			t.Errorf("artifact %q (%s) has no stated reason for its policy", id, a.name)
		}
	}
}

// TestArtifactPathRejectsAnUnknownID keeps the one way into a project directory
// from quietly inventing a file. A typo'd id must fail loudly rather than
// return a path to something with no policy.
func TestArtifactPathRejectsAnUnknownID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if p, err := artifactPath(t.TempDir(), "notes"); err == nil {
		t.Errorf("artifactPath(..., %q) = %q, want an error", "notes", filepath.Base(p))
	}
}

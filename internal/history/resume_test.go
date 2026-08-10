package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	want := Resume{Files: []string{"a.go", "sub/b.go"}, ReadOnly: []string{"ref.md"}, Model: "sonnet"}
	if err := SaveResume(project, want); err != nil {
		t.Fatal(err)
	}
	got := LoadResume(project)
	if got.Model != want.Model || len(got.Files) != 2 || got.Files[1] != "sub/b.go" || len(got.ReadOnly) != 1 {
		t.Errorf("round trip lost data: %+v", got)
	}
	if got.Version != resumeVersion {
		t.Errorf("version = %d, want %d", got.Version, resumeVersion)
	}
	if got.Updated == "" {
		t.Error("updated not stamped")
	}
}

// A resume file is a convenience, not a record: anything unreadable yields the
// zero value and gets overwritten, rather than stopping the user from starting.
// The fixture loader fails loudly on a version mismatch because a wrong fixture
// invalidates a test; here the worst case is retyping.
func TestLoadResumeToleratesJunk(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	p, err := ResumePath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{
		`{`,                                     // malformed
		`{"version": 99, "files": ["a.go"]}`,    // a version we do not know
		`{"files": ["a.go"]}`,                   // no version at all
		`{"version": 1, "files": "not-a-list"}`, // right version, wrong shape
	} {
		if err := os.WriteFile(p, []byte(body), fileMode); err != nil {
			t.Fatal(err)
		}
		if got := LoadResume(project); len(got.Files) != 0 || got.Model != "" {
			t.Errorf("%s should have yielded nothing, got %+v", body, got)
		}
	}

	// A missing file is the ordinary first-run case.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := LoadResume(project); got.Version != 0 {
		t.Errorf("missing file gave %+v", got)
	}
}

// Written through a temp file and a rename, so a crash cannot leave a partial
// resume — and the temp file is created 0600, because the rename replaces the
// destination's inode and a mode set afterwards would not survive.
func TestSaveResumeIsOwnerOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	if err := SaveResume(project, Resume{Files: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := ResumePath(project)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Errorf("resume.json mode = %04o, want %04o", perm, fileMode)
	}
	// Rewriting must replace rather than accumulate, and leave no temp behind.
	if err := SaveResume(project, Resume{Files: []string{"b.go"}}); err != nil {
		t.Fatal(err)
	}
	if got := LoadResume(project); len(got.Files) != 1 || got.Files[0] != "b.go" {
		t.Errorf("rewrite did not replace: %+v", got)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
}

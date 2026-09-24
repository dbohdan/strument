package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session name is a directory name, and it arrives from outside twice over:
// --session is whatever was typed, and `current` is a file an editor can open.
func TestSessionNamesThatWouldReachOutOfTheStateDirectory(t *testing.T) {
	for _, bad := range []string{
		"", "..", ".", "../../etc", "a/b", `a\b`, ".hidden",
		"-leading-dash", "_leading-underscore", "has space", "tab\there",
		"null\x00byte", strings.Repeat("x", maxSessionName+1),
		// Windows names: a trailing dot is dropped, so "spike." is "spike";
		// device names open a device, extension or not, in any case.
		"spike.", "con", "NUL", "nul.txt", "Com1", "lpt9.log", "aux",
	} {
		if err := ValidSessionName(bad); err == nil {
			t.Errorf("ValidSessionName(%q) allowed it", bad)
		}
	}
	for _, good := range []string{"default", "review", "spike-2", "api_v2", "v1.2", "a", "9lives",
		"console", "com10", "com0", "null", "lpt", "auxiliary"} {
		if err := ValidSessionName(good); err != nil {
			t.Errorf("ValidSessionName(%q) = %v, want it allowed", good, err)
		}
	}
}

// The check has to be on the path builder, not only on the flag: a name that
// skipped it would be joined onto a path and escape.
func TestSessionDirRefusesANameThatEscapes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	if p, err := SessionDir(project, "../../../etc"); err == nil {
		t.Errorf("SessionDir escaped to %q", p)
	}
	// The empty name is the documented way to mean "the default one".
	p, err := SessionDir(project, "")
	if err != nil {
		t.Fatalf("the empty name should mean the default session: %v", err)
	}
	if filepath.Base(p) != DefaultSession {
		t.Errorf("SessionDir(\"\") = %q, want the default session", p)
	}
}

// A `current` file edited into something unusable opens the default session
// rather than refusing to start: the sessions are all still on disk either way.
func TestCurrentSessionFallsBackWhenThePointerIsUnusable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	p, err := artifactPath(project, artCurrent)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../../elsewhere", "", "  "} {
		if err := os.WriteFile(p, []byte(bad+"\n"), fileMode); err != nil {
			t.Fatal(err)
		}
		if got := CurrentSession(project); got != DefaultSession {
			t.Errorf("a `current` of %q gave session %q, want the default", bad, got)
		}
	}
}

// seedSession gives a project one session with n segments, each holding turns
// turn rows.
func seedSession(t *testing.T, project, name string, segments, turns int) {
	t.Helper()
	if _, err := EnsureSessionDir(project, name); err != nil {
		t.Fatal(err)
	}
	for i := range segments {
		seg, err := NewLogSegment(project, name, time.Date(2026, 9, 1+i, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.WriteString(`{"type":"session","version":1}` + "\n")
		for range turns {
			b.WriteString(`{"type":"message","role":"user","text":"hi"}` + "\n")
			b.WriteString(`{"type":"turn","outcome":"Success"}` + "\n")
		}
		if err := os.WriteFile(seg, []byte(b.String()), fileMode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListSessionsCountsRunsAndTurns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	seedSession(t, project, "review", 2, 3)
	seedSession(t, project, "spike", 1, 1)
	if err := SetCurrentSession(project, "spike"); err != nil {
		t.Fatal(err)
	}
	// Not a session Strument could have made, so not a session.
	if err := os.WriteFile(filepath.Join(mustSessionsDir(t, project), "not a session"), nil, fileMode); err != nil {
		t.Fatal(err)
	}

	got, err := ListSessions(project)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Session{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if len(got) != 2 {
		t.Fatalf("listed %d sessions, want the 2 real ones: %+v", len(got), got)
	}
	if r := byName["review"]; r.Runs != 2 || r.Turns != 6 {
		t.Errorf("review: %d runs, %d turns; want 2 and 6", r.Runs, r.Turns)
	}
	if !byName["spike"].Current {
		t.Error("the session `current` names was not marked")
	}
	if byName["review"].Current {
		t.Error("a session that is not current was marked")
	}
	if byName["review"].Bytes == 0 {
		t.Error("a session with two segments reported no size")
	}
}

func TestDeleteSessionRemovesOnlyThatSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	seedSession(t, project, "keep", 1, 1)
	seedSession(t, project, "drop", 1, 1)
	// A blob is shared and named by its contents, so deleting one session
	// must not reclaim it.
	hash, err := PutBlob(project, []byte("a payload two sessions might point at"))
	if err != nil {
		t.Fatal(err)
	}

	if err := DeleteSession(project, "drop"); err != nil {
		t.Fatal(err)
	}
	got, err := ListSessions(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "keep" {
		t.Errorf("after deleting one session the listing is %+v", got)
	}
	if _, ok := GetBlob(project, hash); !ok {
		t.Error("deleting a session took a shared blob with it")
	}
	if err := DeleteSession(project, "drop"); err == nil {
		t.Error("deleting a session that is gone should say so")
	}
	if err := DeleteSession(project, "../../etc"); err == nil {
		t.Error("delete accepted a name that escapes")
	}
}

func TestRenameSessionFollowsTheCurrentPointer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	seedSession(t, project, "spike", 1, 2)
	seedSession(t, project, "other", 1, 1)
	if err := SetCurrentSession(project, "spike"); err != nil {
		t.Fatal(err)
	}

	if err := RenameSession(project, "spike", "impl"); err != nil {
		t.Fatal(err)
	}
	if got := CurrentSession(project); got != "impl" {
		t.Errorf("current = %q, want it to follow the rename", got)
	}
	sessions, err := ListSessions(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.Name == "spike" {
			t.Error("the old name is still listed")
		}
		if s.Name == "impl" && s.Turns != 2 {
			t.Errorf("the renamed session has %d turns, want its 2", s.Turns)
		}
	}

	// Two sessions are two conversations, and there is no way to interleave
	// their records that would not invent a history neither of them had.
	if err := RenameSession(project, "impl", "other"); err == nil {
		t.Error("renaming onto an existing session should be refused, not merged")
	}
	if err := RenameSession(project, "gone", "anything"); err == nil {
		t.Error("renaming a session that does not exist should say so")
	}
}

func mustSessionsDir(t *testing.T, project string) string {
	t.Helper()
	d, err := artifactPath(project, artSessions)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The pointer must not outlive what it points at: a `current` naming a
// deleted session would make the next run recreate it empty, under a name the
// user associates with work they had done.
func TestDeletingTheCurrentSessionClearsThePointer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	seedSession(t, project, "spike", 1, 1)
	seedSession(t, project, "other", 1, 1)
	if err := SetCurrentSession(project, "spike"); err != nil {
		t.Fatal(err)
	}

	if err := DeleteSession(project, "other"); err != nil {
		t.Fatal(err)
	}
	if got := CurrentSession(project); got != "spike" {
		t.Errorf("deleting another session moved the pointer to %q", got)
	}
	if err := DeleteSession(project, "spike"); err != nil {
		t.Fatal(err)
	}
	if got := CurrentSession(project); got != DefaultSession {
		t.Errorf("current = %q after deleting it, want the default", got)
	}
}

// And a directory removed by other means — an rm -rf, a partial restore —
// leaves the pointer naming nothing, which reads as the default too.
func TestCurrentSessionFallsBackWhenItsDirectoryIsGone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	if _, err := EnsureProjectDir(project, ""); err != nil {
		t.Fatal(err)
	}
	seedSession(t, project, "spike", 1, 1)
	if err := SetCurrentSession(project, "spike"); err != nil {
		t.Fatal(err)
	}
	dir, err := SessionDir(project, "spike")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := CurrentSession(project); got != DefaultSession {
		t.Errorf("current = %q with its directory gone, want the default", got)
	}
}

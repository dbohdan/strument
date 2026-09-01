package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The temp-dir boundary is the one the sandbox already grants model-run
// commands (sandbox.tempDirs) and the edit path now matches. Both sides
// compare resolved paths: a symlinked root or a symlinked TMPDIR must not
// decide containment by spelling.

func TestUnderTempDirAcceptsThePlatformTempDir(t *testing.T) {
	// A directory *inside* temp, the shape a real scratch path has.
	inner := filepath.Join(os.TempDir(), "strument-underTempDir-test", "x.go")
	if !UnderTempDir(inner) {
		t.Errorf("UnderTempDir(%q) = false; the platform's own temp dir must be granted", inner)
	}
	if !UnderTempDir(os.TempDir()) {
		t.Errorf("UnderTempDir(%q) = false; temp itself counts", os.TempDir())
	}
}

func TestUnderTempDirRejectsOrdinaryPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an out-of-temp root needs a second drive on Windows")
	}
	// t.TempDir is under the granted temp directory, so use the user's home
	// directory for a refusal case instead.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot find a directory outside temp: %v", err)
	}
	outside := filepath.Join(home, "strument-not-temp")
	if UnderTempDir(outside) {
		t.Skipf("user home is under the platform temp directory: %q", home)
	}
	if UnderTempDir(outside) {
		t.Errorf("UnderTempDir(%q) = true; expected false", outside)
	}
}

func TestUnderTempDirRejectsEscapesFromTemp(t *testing.T) {
	// A path under temp that traverses out of it names something the grant
	// does not cover.
	escaped := filepath.Join(os.TempDir(), "..", "outside-of-temp", "x.go")
	if filepath.Clean(escaped) == filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "outside-of-temp", "x.go") {
		if UnderTempDir(escaped) {
			t.Errorf("UnderTempDir(%q) = true; traversal out of temp is not the grant", escaped)
		}
	}
}

// /tmp is granted beside a relocated TMPDIR, matching sandbox.tempDirs: many
// tools write /tmp/... whatever TMPDIR says.
func TestUnderTempDirGrantsSlashTmpBesideAMovedTMPDIR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /tmp on Windows; os.TempDir reads TMP/TEMP and is the only answer there")
	}
	// One TempDir call, captured: a second call makes a *sibling* of the
	// first, and on macOS — where /tmp resolves to /private/tmp while these
	// dirs live under /private/var — that sibling is neither under TMPDIR nor
	// under /tmp. Sibling-of-TMPDIR is not a shape the sandbox grants, and
	// the test's claim is about the TMPDIR dir itself.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if !UnderTempDir("/tmp/strument-x/y.go") {
		t.Error("/tmp was not granted beside a relocated TMPDIR")
	}
	if !UnderTempDir(tmp) {
		t.Error("the relocated TMPDIR itself was not granted")
	}
	if !UnderTempDir(filepath.Join(tmp, "scratch", "x.go")) {
		t.Error("a directory inside the relocated TMPDIR was not granted")
	}
}

// A root that resolves differently from its spelling (macOS /var →
// /private/var, a symlinked TMPDIR) must not break the match — the same
// resolved-against-resolved rule unsafePath's absolute branch already learned
// from CI.
func TestUnderTempDirResolvesBothSides(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// base is under temp; spelled through the link it must still match.
	if !UnderTempDir(filepath.Join(link, "x.go")) {
		t.Errorf("UnderTempDir through a symlinked directory = false for %q", filepath.Join(link, "x.go"))
	}
}

func TestPathInRoot(t *testing.T) {
	root := t.TempDir()
	if !PathInRoot(root, "a/b.go") {
		t.Error("an ordinary repo-relative path reported outside the root")
	}
	if PathInRoot(root, "../escape.go") {
		t.Error("a traversal path reported inside the root")
	}
	if PathInRoot("", "a.go") {
		t.Error("an empty root must refuse everything")
	}
	if !PathInRoot(root, "a.go") {
		t.Error("a bare filename reported outside the root")
	}
}

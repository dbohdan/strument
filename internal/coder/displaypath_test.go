package coder

import (
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// TestDisplayPathThreshold pins the rule /ls, the banner, the pin
// confirmations, and the read-only prompt block all name files by.
//
// One level up is deliberate rather than a tuned constant: it is the project's
// own neighborhood — a file beside the project directory, or one inside a
// sibling of it — where a relative name carries information an absolute one
// throws away. Past that, ../.. counting is only arithmetic the reader has to
// do, and the absolute path is both shorter and truer.
//
// Every fixture hangs off one resolved base, and every absolute expectation is
// built with filepath.Join rather than written as a literal. Both are lessons
// from CI rather than style.
//
// t.TempDir hands back a path that resolution changes under the test —
// /var/folders/... is really /private/var/... on macOS, and the 8.3 form
// C:\Users\RUNNER~1\... is really C:\Users\runneradmin\... on Windows — so an
// expectation taken from it disagreed with an answer that had been through
// absRootPath. Resolving the base first puts the fixture and the answer in one
// namespace.
//
// The Join does two jobs. A literal "/usr/include/foo.h" is not an absolute
// path on Windows, so it was joined to the project root and came back
// "usr/include/foo.h". And Join carries the separator claim: the absolute form
// uses the platform's own separators, because a root-relative name is the
// tool-facing form and must match what read, grep, and ls report — forward
// slashes everywhere — while an absolute name is a path outside the project
// that the user will copy elsewhere. Only Windows can tell those apart, Join
// giving C:\...\notes.md where a ToSlash'd answer would give C:/.../notes.md,
// which is why the claim rides here instead of in a test of its own that would
// read as a tautology on any other host.
func TestDisplayPathThreshold(t *testing.T) {
	// Resolved up front, so a fixture path and the name DisplayPath gives it
	// are in the same namespace. Depth matters too: the far cases need a root
	// with somewhere to be far from.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "work", "code", "proj")

	m := &config.Model{Slug: "test"}
	m.SideModel = m
	c := New(root, m)

	tests := []struct {
		name string
		abs  string
		want string
	}{
		{
			"in the project",
			filepath.Join(root, "a.go"),
			"a.go",
		},
		{
			"nested in the project",
			filepath.Join(root, "internal", "x", "y.go"),
			"internal/x/y.go",
		},
		{
			"beside the project",
			filepath.Join(base, "work", "code", "spec.md"),
			"../spec.md",
		},
		{
			"inside a sibling",
			filepath.Join(base, "work", "code", "sibling", "include", "api.h"),
			"../sibling/include/api.h",
		},
		{
			"two levels up",
			filepath.Join(base, "work", "notes.md"),
			filepath.Join(base, "work", "notes.md"),
		},
		{
			// The shape of the case /read-only is mostly used for: a file with
			// no relationship to the project, several levels away. A real one
			// would be /usr/include/foo.h; this one is under the fixture so the
			// expectation does not depend on the host's directory layout.
			"far away",
			filepath.Join(base, "elsewhere", "include", "foo.h"),
			filepath.Join(base, "elsewhere", "include", "foo.h"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.DisplayPath(tt.abs); got != tt.want {
				t.Errorf("DisplayPath(%q) = %q, want %q", tt.abs, got, tt.want)
			}
		})
	}
}

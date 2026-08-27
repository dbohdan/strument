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
func TestDisplayPathThreshold(t *testing.T) {
	// A root deep enough that a far-away file needs several ..; the fixture has
	// to be able to tell "far" from "near" for the test to mean anything.
	base := t.TempDir()
	root := filepath.Join(base, "work", "code", "proj")

	m := &config.Model{Slug: "test"}
	m.SideModel = m
	c := New(root, m)

	tests := []struct {
		name string
		abs  string
		want string
	}{
		{"in the project", filepath.Join(root, "a.go"), "a.go"},
		{"nested in the project", filepath.Join(root, "internal", "x", "y.go"), "internal/x/y.go"},
		{"beside the project", filepath.Join(base, "work", "code", "spec.md"), "../spec.md"},
		{"inside a sibling", filepath.Join(base, "work", "code", "sibling", "include", "api.h"), "../sibling/include/api.h"},
		{"two levels up", filepath.Join(base, "work", "notes.md"), filepath.Join(base, "work", "notes.md")},
		{"far away", "/usr/include/foo.h", "/usr/include/foo.h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.DisplayPath(tt.abs); got != tt.want {
				t.Errorf("DisplayPath(%q) = %q, want %q", tt.abs, got, tt.want)
			}
		})
	}
}

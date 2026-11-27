//go:build unix

package coder

import (
	"os"
	"path/filepath"
	"testing"
)

// A path that does not exist yet is judged by the directory it would be
// created in, and one the user may not write is refused.
func TestDACRefusesWrite(t *testing.T) {
	dir := t.TempDir()
	if dacRefusesWrite(filepath.Join(dir, "a", "b", "c")) {
		t.Error("a new path under a writable directory was judged refused")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes past the permission bits")
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if !dacRefusesWrite(filepath.Join(locked, "new")) {
		t.Error("a path in a read-only directory was not judged refused")
	}
}

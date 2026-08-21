package repl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayPath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "directory", path: dir, want: dir + "/"},
		{name: "directory with suffix", path: dir + "/", want: dir + "/"},
		{name: "file", path: file, want: file},
		{name: "missing", path: filepath.Join(root, "missing"), want: filepath.Join(root, "missing")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := displayPath(test.path); got != test.want {
				t.Errorf("displayPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

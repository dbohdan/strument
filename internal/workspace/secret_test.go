package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A secret-shaped file the project does not ignore: read refuses it with the
// pattern and the way to allow it, grep skips it and says so in the count, and
// ls still lists it, since a name gives nothing away. Pinning it is the user's
// "this one, yes".
func TestSecretFilesAreNotRead(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", "TOKEN=hunter2\n")
	write(".env.example", "TOKEN=\n")
	write("main.go", "package main // TOKEN\n")

	w := New(root)
	_, err := w.Read(".env", 0, 0)
	if err == nil || !strings.Contains(err.Error(), `".env"`) || !strings.Contains(err.Error(), "/read-only") {
		t.Fatalf("read .env: err = %v, want a refusal naming the pattern and /read-only", err)
	}
	if _, _, _, err := w.openable(".env", 1<<20); err == nil {
		t.Error("openable (read_bin, images) let a secret file through")
	}
	if _, err := w.Read(".env.example", 0, 0); err != nil {
		t.Errorf("read .env.example: %v; the template is exempt", err)
	}

	res, err := w.Grep(GrepQuery{Pattern: "TOKEN", Mode: GrepContent})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		for _, l := range f.Lines {
			if strings.Contains(l.Text, "hunter2") {
				t.Errorf("grep returned a line of .env: %q", l.Text)
			}
		}
	}
	if res.Secret != 1 {
		t.Errorf("grep Secret = %d, want 1", res.Secret)
	}

	entries, _, err := w.List("")
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, e := range entries {
		listed = listed || e.Path == ".env"
	}
	if !listed {
		t.Error("ls hid .env; a name is not a secret")
	}

	abs := filepath.Join(root, ".env")
	w.Pinned = func(p string) bool { return p == abs }
	if ft, err := w.Read(".env", 0, 0); err != nil || len(ft.Lines) != 1 {
		t.Errorf("a pinned secret file should read: %v", err)
	}
}

// "~/" means the home directory, as in a shell: the read tool has no shell to
// expand it, and the model has no other way to learn where home is.
func TestTildeIsTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := expandHome("~/.config/x"); got != filepath.Join(home, ".config", "x") {
		t.Errorf("~/.config/x = %q", got)
	}
	if got := expandHome("~"); got != home {
		t.Errorf("~ = %q", got)
	}
	for _, s := range []string{"~user/x", "a/~/b", "main.go"} {
		if got := expandHome(s); got != s {
			t.Errorf("%q expanded to %q", s, got)
		}
	}
	// A home under the temporary directory is readable by absolute path, so a
	// read through "~/" succeeding is the expansion working.
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ft, err := New(t.TempDir()).Read("~/notes.txt", 0, 0)
	if err != nil || len(ft.Lines) != 1 || ft.Lines[0] != "hi" {
		t.Errorf("read ~/notes.txt: %+v, %v", ft, err)
	}
}

package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// An edit is a read in disguise: a failed one answers with the file's closest
// lines. So a secret-shaped target is refused before any other grant, by
// relative and absolute spelling, and a pinned one goes through. The config's
// additions and exemptions reach the check through ApplyConfig.
func TestSecretFilesAreNotEdited(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=hunter2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	for _, p := range []string{".env", filepath.Join(dir, ".env"), "deploy/.env.production"} {
		if r := c.unsafePath(p); !strings.Contains(r, "secret-file pattern") || !strings.Contains(r, "/add") {
			t.Errorf("%s: reason %q, want the secret-file refusal", p, r)
		}
	}
	if r := c.unsafePath("main.go"); r != "" {
		t.Errorf("main.go: refused with %q", r)
	}

	ApplyConfig(c, &config.Config{SecretFilesAdd: []string{"*.pem"}, SecretFilesExempt: []string{".env"}})
	if r := c.unsafePath("tls/server.pem"); r == "" {
		t.Error("a pattern from secret_files_add was not applied")
	}
	if r := c.unsafePath(".env"); r != "" {
		t.Errorf("a pattern exempted by secret_files_exempt still refused: %q", r)
	}

	ApplyConfig(c, &config.Config{})
	c.AddFile(filepath.Join(dir, ".env"))
	if r := c.unsafePath(".env"); r != "" {
		t.Errorf("a pinned secret file was refused: %q", r)
	}
}

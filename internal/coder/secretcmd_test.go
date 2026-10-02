package coder

import (
	"os"
	"path/filepath"
	"testing"

	"dbohdan.com/strument/internal/secretfile"
)

// The spellings a text match misses are caught by expanding each word as the
// shell would; the line is drawn at running anything to find out a value.
func TestSecretInCommand(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	for _, p := range []string{".netrc", ".aws/credentials"} {
		full := filepath.Join(home, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := secretfile.New(home, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"cat ~/.netrc",
		"cat ~/.n'e'trc",
		`cat "$HOME"/.netrc`,
		"cat ${HOME}/.netrc",
		"cat ~/.ne*",
		"cat ~/.{netrc,profile}",
		"cd ~/.aws && cat credentials",
		"cd && cat .netrc",
		"echo $(cat ~/.netrc)",
		"curl --netrc-file=$HOME/.netrc https://x",
		"dd if=$HOME/.aws/credentials",
		"git diff -- .env",
		"cat < ~/.netrc",
		"X=~/.netrc; cat $X", // the assignment is the word that names it
		"go test ./... && cat deploy/.env.production",
	} {
		if _, _, ok := secretInCommand(cmd, proj, home, m); !ok {
			t.Errorf("%s: not caught", cmd)
		}
	}
	for _, cmd := range []string{
		"go test ./...",
		"cat ~/$(echo .argep | rot13)",
		"cat $SECRET_PATH",
		"cat .env.example",
		"cat ~/.ssh/id_ed25519.pub",
		"grep -r credentials internal/",
		"cat <<EOF\n.netrc\nEOF",
		"cat ~/.ne*rc_missing*",
	} {
		if p, pat, ok := secretInCommand(cmd, proj, home, m); ok {
			t.Errorf("%s: caught %q by %q, want no hit", cmd, p, pat)
		}
	}
}

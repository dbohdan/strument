package history

import (
	"os"
	"testing"
)

// TestMain points the whole package's state root at a temporary directory.
//
// The functions here take a *project* root, which only decides the key a
// project's state is filed under; the state root itself comes from the
// environment. So a test handing over t.TempDir() looks isolated and is not —
// it writes into the real $XDG_STATE_HOME/strument/projects, on the machine of
// whoever ran `go test`. Three such directories were sitting in a developer's
// home directory, each holding an undo stack, named `001-<hash>` after the
// numbered subdirectory t.TempDir() creates.
//
// adopt_test.go already set XDG_STATE_HOME per test and was clean. Doing it
// here instead makes isolation the default for the package, so a new test
// cannot leak by forgetting — which is what every test that leaked had done.
// A test that wants its own state root still calls t.Setenv and overrides this.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "strument-history-state")
	if err != nil {
		panic("history tests need a temporary state root: " + err.Error())
	}
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

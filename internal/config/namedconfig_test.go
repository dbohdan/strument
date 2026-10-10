package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// A config path someone named and got wrong is a typo, not a first run: the
// error names the file and does not show the first-run screen.
func TestNamedMissingUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tester.star")
	_, err := Load(Options{UserConfigPath: path})
	if err == nil || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "no configuration file yet") {
		t.Errorf("err = %v, want one naming %s without the first-run text", err, path)
	}
}

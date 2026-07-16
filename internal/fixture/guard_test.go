package fixture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Fixtures are committed; keys are not (fixture-harness §1, §5). This test
// fails if anything under testdata/ smells like a credential.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-or-v1-[0-9a-f]{8}`),          // OpenRouter keys
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),         // OpenAI-style keys
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]{16,}`), // bearer tokens
	regexp.MustCompile(`(?i)"(authorization|api-key|x-api-key|cookie|set-cookie)"\s*:`),
}

func TestNoSecretsInTestdata(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("no testdata directory yet")
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, pat := range secretPatterns {
			if loc := pat.FindIndex(data); loc != nil {
				start := max(loc[0]-40, 0)
				end := min(loc[1]+20, len(data))
				t.Errorf("%s: matches secret pattern %q near: %q", path, pat, data[start:end])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

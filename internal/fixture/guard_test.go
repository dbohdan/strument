package fixture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Fixtures are committed; keys are not. This test
// fails if anything under testdata/fixtures/ smells like a credential.
// (testdata/transliterated/ holds aider's own public test corpus, which
// contains placeholder bearer tokens in code examples — out of scope.)
var secretPatterns = append(keyPatterns,
	regexp.MustCompile(`(?i)"(authorization|api-key|x-api-key|cookie|set-cookie)"\s*:`),
)

// keyPatterns match a credential's value rather than a header's name.
var keyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-or-v1-[0-9a-f]{8}`),                // OpenRouter keys
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),               // OpenAI-style keys
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]{16,}`), // bearer tokens
}

func TestNoSecretsInTestdata(t *testing.T) {
	scanForSecrets(t, filepath.Join("..", "..", "testdata", "fixtures"), secretPatterns)
}

// Experiments are where a live key is in use while files get written: runner
// scripts, pty captures, and JSONL transcripts that are kept on purpose. The
// header-name pattern stays out, because a runner that builds its own request
// names "Authorization" legitimately; what must never appear is the value.
func TestNoSecretsInExperiments(t *testing.T) {
	scanForSecrets(t, filepath.Join("..", "..", "doc", "experiments"), keyPatterns)
}

func scanForSecrets(t *testing.T, root string, patterns []*regexp.Regexp) {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skipf("no %s directory", root)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, pat := range patterns {
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

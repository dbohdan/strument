package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The grammar was cut against real files rather than against the format's
// specification, so real files are what has to keep parsing. This walks every
// SKILL.md under a root given by STRUMENT_SKILL_CORPUS and requires all of
// them to parse — a bare regression check on the one thing a smaller grammar
// can get wrong.
//
// Skipped when the variable is unset, because the suite must run offline and
// without fixtures nobody has. To run it against the skills on a machine:
//
//	STRUMENT_SKILL_CORPUS=/mnt/skills go test ./internal/skill/ -run Corpus -v
//
// The corpus this was developed against was 67 files: all had name and
// description, 34 had license, 3 had compatibility, one used a block scalar,
// and none used metadata or any invocation-control field.
func TestParsesRealCorpus(t *testing.T) {
	root := os.Getenv("STRUMENT_SKILL_CORPUS")
	if root == "" {
		t.Skip("set STRUMENT_SKILL_CORPUS to a directory of skills to run this")
	}

	var found, failed, unreachable int
	fields := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		// Counted rather than swallowed. A corpus entry this test cannot reach
		// is one it did not check, and a silent skip would make the coverage
		// figure below a claim the run did not earn.
		if walkErr != nil {
			unreachable++
			t.Logf("could not walk %s: %v", path, walkErr)
			return nil
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		found++
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("%s: unreadable: %v", path, readErr)
			return nil
		}
		fm, body, parseErr := Parse(string(src))
		if parseErr != nil {
			failed++
			t.Errorf("%s: %v", path, parseErr)
			return nil
		}
		// A parse that succeeds but drops the required fields would pass a
		// bare error check while breaking every caller.
		if fm.Name == "" || fm.Description == "" {
			t.Errorf("%s: parsed but name=%q description=%q", path, fm.Name, fm.Description)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s: parsed but the body came back empty", path)
		}
		for name, v := range map[string]string{
			"name": fm.Name, "description": fm.Description, "license": fm.License,
			"compatibility": fm.Compatibility, "allowed-tools": fm.AllowedTools,
		} {
			if v != "" {
				fields[name]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatalf("no SKILL.md found under %s", root)
	}
	t.Logf("%d files, %d failed to parse, %d unreachable; fields present: %v",
		found, failed, unreachable, fields)
}

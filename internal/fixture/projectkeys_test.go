package fixture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every setting a project config can change has to be classified in
// internal/config/inspect.go, because that classification is what `strument
// trust` shows the user before recording a grant. A key nobody classified is a
// key that silently skips the disclosure — the file gets trusted and its
// effect is never named.
//
// Like applyconfig_test.go, this checks the shape of the rule rather than a
// list of keys, for the same reason: a test enumerating today's twenty-nine
// would pass on the thirtieth.
var (
	// The merge block's arms: `if project.hasEnvAllow {`. That block is the
	// single definition of what a project config is allowed to change.
	mergeArm = regexp.MustCompile(`^\s*if project\.(has[A-Za-z0-9]+) \{`)
	// A classification table entry, `"hasEnvAllow": {` or `…: promptKey(`.
	tableEntry = regexp.MustCompile(`^\s*"(has[A-Za-z0-9]+)":`)
	// A line of projectKeyOrder, `"hasEnvAllow",`. Being in the table without
	// being in the order means the key is classified and never printed, which
	// looks exactly like being classified.
	orderEntry = regexp.MustCompile(`^\s*"(has[A-Za-z0-9]+)",`)
	// The user-facing name a table entry prints, in the two shapes it is
	// written in: a `name:` field, and promptKey's first argument.
	entryName   = regexp.MustCompile(`name:\s*"([a-z_]+)"`)
	promptKeyed = regexp.MustCompile(`promptKey\("([a-z_]+)"`)
)

// modelsIsNotAFlag: `models` is merged with maps.Copy rather than behind a
// has* flag, so the pattern above cannot see it — and it is the most powerful
// key in the file, since a redefined alias sends the conversation to any
// base_url with a key from the user's own environment. It is classified under
// its own name, and named here so its absence from the merge arms is a
// recorded fact rather than a gap.
const modelsIsNotAFlag = "models"

// minMergeArms is the counter-arm. If the pattern stops matching the merge
// block, every assertion below passes by checking nothing — which is the
// failure this test exists to prevent. There are 29 arms today.
const minMergeArms = 25

func TestEveryProjectKeyIsClassified(t *testing.T) {
	root := repoRoot(t)
	arms := matchLines(t, filepath.Join(root, "internal", "config", "load.go"), mergeArm)
	table := matchLines(t, filepath.Join(root, "internal", "config", "inspect.go"), tableEntry)
	order := matchLines(t, filepath.Join(root, "internal", "config", "inspect.go"), orderEntry)

	if len(arms) < minMergeArms {
		t.Fatalf("the merge-block pattern matched %d arms, fewer than the %d there have been; "+
			"it has stopped recognising internal/config/load.go and this check is vacuous",
			len(arms), minMergeArms)
	}

	for _, key := range arms {
		if !slices.Contains(table, key) {
			t.Errorf("a project config can set %s and internal/config/inspect.go does not classify it.\n"+
				"  Add a projectKeys entry: a detail function if trusting it grants something, "+
				"nil if its worst case is an annoyance.", key)
		}
		if !slices.Contains(order, key) {
			t.Errorf("%s is classified but missing from projectKeyOrder, so `strument trust` "+
				"would never print it.", key)
		}
	}
	for _, key := range table {
		if !slices.Contains(arms, key) {
			t.Errorf("internal/config/inspect.go classifies %s, which the merge block in "+
				"internal/config/load.go no longer applies. Remove it.", key)
		}
	}
	for _, key := range order {
		if !slices.Contains(table, key) {
			t.Errorf("projectKeyOrder names %s, which projectKeys does not classify.", key)
		}
	}

	// The name the summary prints has to be the name the user types. It is not
	// derivable from the flag field — hasWebSearch is spelled `websearch` in a
	// config, hasEnvAllow is `env_allow` — so it is written out, and a written
	// name can be wrong. It was: `web_search`, for a key that does not exist.
	// Every name must appear as a string literal in load.go, which is where
	// every global is read by name.
	loadSrc, err := os.ReadFile(filepath.Join(root, "internal", "config", "load.go"))
	if err != nil {
		t.Fatal(err)
	}
	inspectPath := filepath.Join(root, "internal", "config", "inspect.go")
	names := append(matchLines(t, inspectPath, entryName), matchLines(t, inspectPath, promptKeyed)...)
	if len(names) < minMergeArms {
		t.Fatalf("found %d key names in internal/config/inspect.go, fewer than the %d keys there are; "+
			"the name pattern has stopped matching and this check is vacuous", len(names), minMergeArms)
	}
	for _, n := range names {
		if !strings.Contains(string(loadSrc), `"`+n+`"`) {
			t.Errorf("internal/config/inspect.go calls a key %q, which internal/config/load.go never "+
				"reads by that name. `strument trust` would name a setting nobody can write.", n)
		}
	}

	// The exception carries its own assertion, so deleting the entry is a
	// failure rather than a silent narrowing of the disclosure.
	full := matchLines(t, filepath.Join(root, "internal", "config", "inspect.go"),
		regexp.MustCompile(`^\s*"([a-zA-Z]+)":`))
	if !slices.Contains(full, modelsIsNotAFlag) {
		t.Errorf("internal/config/inspect.go no longer classifies %q, which has no has* flag "+
			"and so is invisible to every other check here.", modelsIsNotAFlag)
	}
}

func matchLines(t *testing.T, path string, re *regexp.Regexp) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for line := range splitLines(string(body)) {
		if m := re.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func splitLines(s string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for start := 0; start <= len(s); {
			end := start
			for end < len(s) && s[end] != '\n' {
				end++
			}
			if !yield(s[start:end]) {
				return
			}
			start = end + 1
		}
	}
}

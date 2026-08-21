//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestWriteRuleMatchesTheKind is the test that would have caught the bug a
// live run found instead.
//
// Landlock's directory rights include make_dir and remove_dir, and asking for
// them on a regular file is refused with "inconsistent access rights". That
// error fails the *whole* ruleset, so the consequence is not a narrower
// sandbox — it is no sandbox, silently, with every other rule discarded too.
// /dev/null is a character device, and granting it as a directory disabled
// confinement entirely.
//
// It needs no Landlock to run: the classification is just a stat.
func TestWriteRuleMatchesTheKind(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	dirRule, ok := writeRule(dir)
	if !ok {
		t.Fatal("a directory produced no rule")
	}
	fileRule, ok := writeRule(file)
	if !ok {
		t.Fatal("a file produced no rule")
	}

	// The rights are what the kernel validates, so the rights are what to
	// assert on — not the Go type, which would still compile if it were wrong.
	if !strings.Contains(dirRule.String(), "make_dir") {
		t.Errorf("a directory rule lacks directory rights: %s", dirRule)
	}
	if strings.Contains(fileRule.String(), "make_dir") {
		t.Errorf("a file rule carries directory rights, which the kernel refuses "+
			"and which fails the entire ruleset: %s", fileRule)
	}
	// refer is about reparenting entries within a directory; on a file it is
	// the same category error.
	if strings.Contains(fileRule.String(), "refer") {
		t.Errorf("a file rule asks for refer: %s", fileRule)
	}
}

// TestWriteRuleSkipsMissingPaths: a machine without /dev/shm, or a project
// with no state directory yet, must not fail the whole policy.
func TestWriteRuleSkipsMissingPaths(t *testing.T) {
	if _, ok := writeRule(filepath.Join(t.TempDir(), "absent")); ok {
		t.Error("a missing path produced a rule; Landlock would refuse the ruleset")
	}
}

// TestRulesAlwaysReadTheWholeFilesystem: reads and execution are unconfined by
// design, and that rule is what makes every binary on the machine work without
// enumerating a single directory.
func TestRulesAlwaysReadTheWholeFilesystem(t *testing.T) {
	rules := Policy{Writable: []string{t.TempDir()}}.rules()
	if len(rules) == 0 {
		t.Fatal("no rules at all")
	}
	first := fmt.Sprint(rules[0])
	if !strings.Contains(first, "[/]") {
		t.Errorf("the first rule is not the read rule over /: %s", first)
	}
	if !strings.Contains(first, "execute") {
		t.Errorf("the read rule does not grant execution, so nothing would run: %s", first)
	}
}

// TestRulesSkipDevicesThatAreNotThere keeps the policy portable across the
// container and VM kernels where parts of /dev are absent.
func TestRulesSkipDevicesThatAreNotThere(t *testing.T) {
	for _, rule := range (Policy{Writable: []string{t.TempDir()}}).rules() {
		for path := range strings.FieldsSeq(fmt.Sprint(rule)) {
			if strings.HasPrefix(path, "[/dev/") {
				name := strings.Trim(path, "[]")
				if _, err := os.Stat(name); err != nil {
					t.Errorf("%s is in the ruleset but not on this machine", name)
				}
			}
		}
	}
}

// TestGrantedMatchesTheRules pins the agreement between what /sandbox reports
// and what the kernel is actually told.
//
// These are two separate walks over the same list — Granted stats each path,
// rules() builds one rule per path that writeRule accepts — and they are the
// difference between a report and a policy. A live run found them disagreeing
// in the direction that matters: /sandbox listed a cache directory that had
// never been created, directly above a build the sandbox then refused
// permission to create it.
func TestGrantedMatchesTheRules(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "there")
	if err := os.MkdirAll(present, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "not-there")

	p := Policy{Writable: []string{present, file, absent}}

	granted := p.Granted()
	want := []string{present, file}
	if !slices.Equal(granted, want) {
		t.Errorf("Granted() = %v, want %v", granted, want)
	}

	// Every granted path produces a rule, and nothing else does. Counted
	// against the rule set the kernel receives, minus the read rule over "/"
	// and whichever devices this machine happens to have.
	var built int
	for _, path := range p.Writable {
		if _, ok := writeRule(path); ok {
			built++
		}
	}
	if built != len(granted) {
		t.Errorf("writeRule accepted %d paths, Granted() reported %d", built, len(granted))
	}
}

package fixture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every config value reaches a Coder through coder.ApplyConfig, and this checks
// the shape of that rule rather than a list of fields — because a list is what
// failed. `chatCmd.Run` and `cmdReload` each carried their own copy of the
// assignments, and they drifted three times: a named check you could edit and
// reload to no effect, egress ports left alone so a new proxy never took, and
// the five prompt_* keys that went in with the feature and were never added to
// reload at all. Ten settings were stale by the time anyone counted.
//
// A test enumerating the ten would have passed on the eleventh. So instead:
// assigning a config field to a Coder field anywhere but applyconfig.go is the
// defect, whatever the field is called.
var (
	// `<recv>.<Field> = … cfg.<Field> …`, which is the startup-code shape.
	assignFromCfg = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_.]*\.[A-Z][A-Za-z0-9]*\s*(=|\+=)[^=].*\bcfg\.`)
	// The receivers that are a Coder. A config assigned to something else —
	// an Output's display setting, a Repo's signing key — is a different object
	// with its own owner, and ApplyConfig cannot reach it.
	coderRecv = regexp.MustCompile(`^\s*(cdr|c|r\.coder)\.`)
)

// Receivers that are deliberately assigned outside ApplyConfig, each with the
// reason it cannot move there.
var allowedOutside = map[string]string{
	"cdr.Sandbox":         "Landlock is monotonic: the writable set is fixed when the process starts",
	"cdr.Sandbox.Skipped": "part of the same startup-only sandbox state",
	"cdr.Scrape":          "a port built around a closure, rebuilt by the REPL's ApplyEgress hook",
}

func TestConfigReachesTheCoderInOnePlace(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	checked := 0

	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			// The one legitimate home, and the config package's own merging.
			if rel == filepath.Join("internal", "coder", "applyconfig.go") ||
				strings.HasPrefix(rel, filepath.Join("internal", "config")) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(body), "\n") {
				if !assignFromCfg.MatchString(line) || !coderRecv.MatchString(line) {
					continue
				}
				checked++
				lhs := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
				if _, ok := allowedOutside[lhs]; ok {
					continue
				}
				offenders = append(offenders,
					rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	// The counter-arm: if the pattern stops matching anything at all, this test
	// passes by finding nothing to check, which is the failure it is here to
	// prevent. The allowed receivers are the floor.
	if checked < len(allowedOutside) {
		t.Fatalf("the assignment pattern matched %d lines, fewer than the %d known "+
			"exceptions; it has stopped recognising startup code and this check is vacuous",
			checked, len(allowedOutside))
	}
	for _, o := range offenders {
		t.Errorf("config assigned to a Coder outside coder.ApplyConfig:\n  %s\n"+
			"  Put it in internal/coder/applyconfig.go so /reload applies it too, "+
			"or add it to allowedOutside with the reason it cannot move.", o)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

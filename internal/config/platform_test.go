package config

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// evalConfigExpr runs one line of config source against exactly the globals a
// real config file sees, and returns the value it bound to `got`.
func evalConfigExpr(t *testing.T, src string) (starlark.Value, error) {
	t.Helper()
	thread := &starlark.Thread{Name: "test"}
	opts := &syntax.FileOptions{Set: true, TopLevelControl: true}
	globals, err := starlark.ExecFileOptions(opts, thread, "config.star", src,
		predeclaredGlobals(func(string) (string, bool) { return "", false }, t.TempDir()))
	if err != nil {
		return nil, err
	}
	return globals["got"], nil
}

func evalString(t *testing.T, src string) string {
	t.Helper()
	v, err := evalConfigExpr(t, src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	s, ok := starlark.AsString(v)
	if !ok {
		t.Fatalf("%s produced %v, not a string", src, v)
	}
	return s
}

// TestPlatformSpeaksPython pins the translation, which is the whole point of
// the object: a config author writes what CPython would give them, not what Go
// calls it. `platform.system == "linux"` silently never matching is the bug
// this exists to prevent.
func TestPlatformSpeaksPython(t *testing.T) {
	got := evalString(t, `got = "landlock" if platform.system == "Linux" else ""`)
	want := ""
	if runtime.GOOS == "linux" {
		want = "landlock"
	}
	if got != want {
		t.Errorf("got %q, want %q on %s", got, want, runtime.GOOS)
	}
}

func TestPlatformAttributes(t *testing.T) {
	host, _ := os.Hostname()
	linux := runtime.GOOS == "linux"
	for _, tc := range []struct {
		attr string
		ok   func(string) bool
		why  string
	}{
		{"system", func(v string) bool { return !linux || v == "Linux" }, "capitalized, never the raw GOOS"},
		{"bits", func(v string) bool { return v == "64bit" || v == "32bit" }, "CPython spells it 64bit"},
		{"machine", func(v string) bool { return !linux || (v != "" && v != "amd64" && v != "arm64") },
			"uname's spelling (x86_64), never Go's (amd64)"},
		{"node", func(v string) bool { return v == host }, "the hostname"},
		{"release", func(v string) bool { return !linux || v != "" }, "uname release"},
		{"version", func(v string) bool { return !linux || v != "" }, "uname version"},
	} {
		got := evalString(t, "got = platform."+tc.attr)
		if !tc.ok(got) {
			t.Errorf("platform.%s = %q; want %s", tc.attr, got, tc.why)
		}
	}
}

// TestPlatformOmitsUntranslatableFields pins the deliberate absences. A
// plausible wrong value is worse than an error: platform() is a composite with
// no faithful translation, and processor() is empty even in CPython on Linux.
func TestPlatformOmitsUntranslatableFields(t *testing.T) {
	for _, attr := range []string{"platform", "processor"} {
		_, err := evalConfigExpr(t, "got = platform."+attr)
		if err == nil {
			t.Errorf("platform.%s resolved; it should be absent rather than faked", attr)
			continue
		}
		if !strings.Contains(err.Error(), attr) {
			t.Errorf("the error for platform.%s does not name it: %v", attr, err)
		}
	}
}

// TestPlatformIsReadOnly: these are facts about the host, not settings.
func TestPlatformIsReadOnly(t *testing.T) {
	if _, err := evalConfigExpr(t, `platform.system = "Haiku"`); err == nil {
		t.Error("platform.system was assignable")
	}
}

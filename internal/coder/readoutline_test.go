package coder

import (
	"slices"
	"strings"
	"testing"
)

// withReadArm sets the read-outline trial's arm for one test.
func withReadArm(t *testing.T, arm string) {
	t.Helper()
	old := readArm
	readArm = arm
	t.Cleanup(func() { readArm = old })
}

// A Python file with a class past line 30, for windows that stop short of it.
func outlineFile() string {
	var b strings.Builder
	for range 30 {
		b.WriteString("# filler\n")
	}
	b.WriteString("class Store:\n    def get(self, key):\n        return key\n\n\ndef make(n):\n    return Store()\n")
	return b.String()
}

func TestReadOutlineArms(t *testing.T) {
	src := map[string]string{"m.py": outlineFile()}

	t.Run("A: a partial read says where it stopped and nothing more", func(t *testing.T) {
		withReadArm(t, "A")
		c, _ := observeEnv(t, src)
		got := readTool(c, call("read", `{"path":"m.py","limit":10}`))
		if strings.Contains(got, "Outside these lines") {
			t.Errorf("the baseline must not outline:\n%s", got)
		}
	})
	t.Run("B: a partial read outlines the rest", func(t *testing.T) {
		withReadArm(t, "B")
		c, _ := observeEnv(t, src)
		got := readTool(c, call("read", `{"path":"m.py","limit":10}`))
		for _, want := range []string{"Outside these lines", "- class Store  [31-33]", "  - def get(self, key)  [32-33]", "- def make(n)  [36-37]"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q:\n%s", want, got)
			}
		}
		whole := readTool(c, call("read", `{"path":"m.py"}`))
		if strings.Contains(whole, "Outside these lines") {
			t.Errorf("a whole read has no rest to outline:\n%s", whole)
		}
	})
	t.Run("C: outline: true returns the map alone", func(t *testing.T) {
		withReadArm(t, "C")
		c, _ := observeEnv(t, src)
		got := readTool(c, call("read", `{"path":"m.py","outline":true}`))
		if !strings.Contains(got, "- class Store  [31-33]") || strings.Contains(got, "# filler") {
			t.Errorf("want the outline and not the contents:\n%s", got)
		}
		if got := readTool(c, call("read", `{"path":"nope.py","outline":true}`)); !strings.Contains(got, "Could not read") {
			t.Errorf("a missing file should say so:\n%s", got)
		}
	})
	t.Run("D: a read without limit is sent back", func(t *testing.T) {
		withReadArm(t, "D")
		c, _ := observeEnv(t, src)
		if got := readTool(c, call("read", `{"path":"m.py"}`)); !strings.Contains(got, "Give limit") {
			t.Errorf("want the request for a limit:\n%s", got)
		}
	})
}

// The schema follows the arm: nothing new in A and B, the outline parameter
// from C, and limit required in D.
func TestReadSchemaFollowsTheArm(t *testing.T) {
	for arm, want := range map[string][2]bool{"A": {false, false}, "B": {false, false}, "C": {true, false}, "D": {true, true}} {
		withReadArm(t, arm)
		def := readToolDef()
		props, _ := def.Parameters["properties"].(map[string]any)
		required, _ := def.Parameters["required"].([]any)
		_, hasOutline := props["outline"]
		if hasOutline != want[0] || slices.Contains(required, any("limit")) != want[1] {
			t.Errorf("arm %s: outline %v, limit required %v; want %v", arm, hasOutline,
				slices.Contains(required, any("limit")), want)
		}
	}
}

package coder

import (
	"context"
	"strings"
	"testing"
)

// TestCodeArms: each arm installs what it claims, and the baseline nothing.
func TestCodeArms(t *testing.T) {
	defer func(old string) { codeArm = old }(codeArm)
	probe := `JSON.stringify([typeof Object.groupBy, typeof Map.groupBy, typeof _,
		typeof Object.groupBy === "function" ? Object.groupBy([1,2,3,4], x => x % 2 ? "odd" : "even") : null,
		typeof _ === "function" ? _.countBy(["a","b","a"]) : null])`
	for _, tc := range []struct{ arm, want, doc string }{
		{"", `["undefined","undefined","undefined",null,null]`, ""},
		{"polyfill", `["function","function","undefined",{"odd":[1,3],"even":[2,4]},null]`, ""},
		{"lodash", `["function","function","function",{"odd":[1,3],"even":[2,4]},{"a":2,"b":1}]`, "Lodash 4 is loaded as _"},
	} {
		codeArm = tc.arm
		c := testCoder(t)
		c.Out = &captureOut{}
		got := c.runCode(context.Background(), codeCall{code: probe})
		if !strings.Contains(got, tc.want) {
			t.Errorf("arm %q: got %s, want %s", tc.arm, got, tc.want)
		}
		desc := codeTool(nil).Description
		if (tc.doc == "") != !strings.Contains(desc, "Lodash") || (tc.doc != "" && !strings.Contains(desc, tc.doc)) {
			t.Errorf("arm %q: description line wrong", tc.arm)
		}
	}
}

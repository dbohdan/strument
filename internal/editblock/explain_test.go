package editblock

import (
	"strings"
	"testing"
)

// ExplainLoose names what the line matcher forgave, with the numbers. The
// first two rows are the two miscounts from the GPT-6 Luna run that prompted
// it: one tab too many in a Go file, three too few in a deep templ one.
func TestExplainLoose(t *testing.T) {
	goFile := "func f() {\n\tif x {\n\t\tfor {\n\t\t\tgroups := g()\n\t\t\tcomp := c(groups)\n\t\t}\n\t}\n}\n"
	deep := strings.Repeat("\t", 9) + "case \"A\":\n" + strings.Repeat("\t", 10) + "<article>\n"
	for _, tc := range []struct {
		name, content, search, want string
	}{
		{"one tab too many", goFile, "\t\t\t\tgroups := g()\n\t\t\t\tcomp := c(groups)\n",
			"line 4 has 3 tabs, and you sent 4"},
		{"three tabs too few", deep, strings.Repeat("\t", 6) + "case \"A\":\n" + strings.Repeat("\t", 7) + "<article>\n",
			"line 1 has 9 tabs, and you sent 6"},
		{"spaces for tabs", goFile, "        groups := g()\n",
			"line 4 has 3 tabs, and you sent 8 spaces"},
		{"no indentation sent", goFile, "groups := g()\ncomp := c(groups)\n",
			"line 4 has 3 tabs, and you sent no indentation"},
		{"leading blank line", goFile, "\n\t\tgroups := g()\n\t\tcomp := c(groups)\n",
			"line 4 has 3 tabs, and you sent 2"},
		{"mixed indentation", "a\n\t  b\n", "  b\n",
			`line 2 has "\t  ", and you sent 2 spaces`},
		{"elision", "one\ntwo\nthree\n", "one\n...\nthree\n",
			`"..." lines`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExplainLoose(tc.content, tc.search); !strings.Contains(got, tc.want) {
				t.Errorf("ExplainLoose = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

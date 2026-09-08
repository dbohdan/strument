package coder

import (
	"context"
	"strings"
	"testing"
)

func TestScratchPrelude(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"a.py": "x = 1  # NEEDLE\n"})
	pre := "class _Tools:\n    read = read\n    grep = grep\n    glob = glob\n    ls = ls\n" +
		"    def __str__(self):\n        return \"read(path) grep(pattern) glob(pattern) ls(path)\"\n" +
		"tools = _Tools()\n"
	for _, tc := range []struct{ name, code string }{
		{"namespace call", pre + "tools.read(path=\"a.py\")"},
		{"print(tools)", pre + "print(tools)"},
		{"bare name still works", pre + "read(path=\"a.py\")"},
		{"traceback offset", pre + "boom"},
	} {
		got := c.runCode(context.Background(), codeCall{code: tc.code})
		t.Logf("%-24s -> %s", tc.name, strings.ReplaceAll(got, "\n", " ⏎ ")[:min(190, len(got))])
	}
}

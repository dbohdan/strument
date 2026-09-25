package repomap

import (
	"strings"
	"testing"
)

// Methods are listed under their class, with signatures; a function's own
// helpers are not. The old depth rule kept Go's methods and dropped every
// Python, TypeScript and Rust method.
func TestDefOutlinesListMembersNotLocals(t *testing.T) {
	for _, tc := range []struct {
		file, src string
		want      []string // "depth signature"
		not       string
	}{
		{"a.py", "class Foo(Base):\n    def bar(self, a):\n        def inner():\n            pass\n        return a\n\n    async def baz(\n        self,\n        b: int,\n    ) -> int:  # note\n        return 1\n\ndef top(a, b=2):\n    return a\n",
			[]string{"0 class Foo(Base)", "1 def bar(self, a)", "1 async def baz(self, b: int) -> int", "0 def top(a, b=2)"}, "inner"},
		{"a.ts", "export class Store {\n  get(key: string): number {\n    return 1;\n  }\n}\nexport function make(n: number): Store {\n  return new Store();\n}\n",
			[]string{"0 class Store", "1 get(key: string): number", "0 function make(n: number): Store"}, ""},
		{"a.rs", "struct P { x: i32 }\nimpl P {\n    pub fn new(x: i32) -> Self { P { x } }\n}\nfn main() {}\n",
			[]string{"0 struct P", "0 pub fn new(x: i32) -> Self", "0 fn main()"}, ""},
	} {
		defs, ok := DefOutlines(tc.file, []byte(tc.src))
		if !ok {
			t.Fatalf("%s did not parse", tc.file)
		}
		var got []string
		for _, d := range defs {
			got = append(got, string(rune('0'+d.Depth))+" "+d.Signature)
			if tc.not != "" && d.Name == tc.not {
				t.Errorf("%s: a local helper was listed: %+v", tc.file, d)
			}
		}
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", tc.file, got, tc.want)
		}
	}
}

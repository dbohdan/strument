// Replays smd.js's own test suite: all test_single_write cases from
// smd_test.js, extracted verbatim to smd-cases.json, each run both as one
// Write and char by char (mirroring smd_test_setup.js).

package render

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// treeNode mirrors smd_test_setup.js's Test_Renderer_Node: children are
// strings or nested nodes, adjacent text children are coalesced.
type treeNode struct {
	typ      Token
	attrs    map[Attr]string
	children []any // string | *treeNode
}

// treeRenderer builds the tree that the fixtures are compared against.
type treeRenderer struct {
	root  *treeNode
	stack []*treeNode
}

func newTreeRenderer() *treeRenderer {
	root := &treeNode{typ: Document}
	return &treeRenderer{root: root, stack: []*treeNode{root}}
}

func (r *treeRenderer) cur() *treeNode { return r.stack[len(r.stack)-1] }

func (r *treeRenderer) AddToken(t Token) {
	n := &treeNode{typ: t}
	cur := r.cur()
	cur.children = append(cur.children, n)
	r.stack = append(r.stack, n)
}

func (r *treeRenderer) EndToken() {
	if len(r.stack) == 1 {
		panic("EndToken on root")
	}
	r.stack = r.stack[:len(r.stack)-1]
}

func (r *treeRenderer) AddText(text string) {
	cur := r.cur()
	if n := len(cur.children); n > 0 {
		if s, ok := cur.children[n-1].(string); ok {
			cur.children[n-1] = s + text
			return
		}
	}
	cur.children = append(cur.children, text)
}

func (r *treeRenderer) SetAttr(a Attr, value string) {
	cur := r.cur()
	if cur.attrs == nil {
		cur.attrs = map[Attr]string{}
	}
	cur.attrs[a] = value
}

// smdCase is one extracted test_single_write invocation.
type smdCase struct {
	Title    string            `json:"title"`
	Markdown string            `json:"markdown"`
	Expected []json.RawMessage `json:"expected"`
}

// decodeExpected converts the JSON tree ({type, attrs?, children} | string)
// into treeNode form.
func decodeExpected(t *testing.T, raw json.RawMessage) any {
	t.Helper()

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var obj struct {
		Type     int32             `json:"type"`
		Attrs    map[string]string `json:"attrs"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("bad expected node: %v", err)
	}

	n := &treeNode{typ: Token(obj.Type)}
	if len(obj.Attrs) > 0 {
		n.attrs = map[Attr]string{}
		for k, v := range obj.Attrs {
			num, err := strconv.Atoi(k)
			if err != nil {
				t.Fatalf("bad attr key %q", k)
			}
			n.attrs[Attr(num)] = v
		}
	}
	for _, c := range obj.Children {
		n.children = append(n.children, decodeExpected(t, c))
	}
	return n
}

func nodesEqual(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok || bok {
		return aok && bok && as == bs
	}

	an := a.(*treeNode) //nolint:forcetypeassert // children are string | *treeNode
	bn := b.(*treeNode) //nolint:forcetypeassert
	if an.typ != bn.typ || len(an.children) != len(bn.children) || len(an.attrs) != len(bn.attrs) {
		return false
	}
	for k, v := range an.attrs {
		if bv, ok := bn.attrs[k]; !ok || bv != v {
			return false
		}
	}
	for i := range an.children {
		if !nodesEqual(an.children[i], bn.children[i]) {
			return false
		}
	}
	return true
}

func dumpChildren(sb *strings.Builder, children []any, depth int) {
	for _, c := range children {
		sb.WriteString(strings.Repeat("  ", depth))
		switch c := c.(type) {
		case string:
			fmt.Fprintf(sb, "%q\n", c)
		case *treeNode:
			sb.WriteString(c.typ.String())
			if len(c.attrs) > 0 {
				fmt.Fprintf(sb, " %v", c.attrs)
			}
			sb.WriteString("\n")
			dumpChildren(sb, c.children, depth+1)
		}
	}
}

func dumpTree(children []any) string {
	var sb strings.Builder
	dumpChildren(&sb, children, 0)
	return sb.String()
}

func loadCases(t *testing.T) []smdCase {
	t.Helper()
	data, err := os.ReadFile("../../testdata/transliterated/render/smd-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []smdCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 400 {
		t.Fatalf("only %d cases; extraction incomplete?", len(cases))
	}
	return cases
}

func runCase(t *testing.T, tc smdCase, byChar bool) {
	t.Helper()
	r := newTreeRenderer()
	p := NewParser(r)
	if byChar {
		for _, c := range tc.Markdown {
			p.Write(string(c))
		}
	} else {
		p.Write(tc.Markdown)
	}
	p.End()

	expected := make([]any, 0, len(tc.Expected))
	for _, raw := range tc.Expected {
		expected = append(expected, decodeExpected(t, raw))
	}

	equal := len(r.root.children) == len(expected)
	if equal {
		for i := range expected {
			if !nodesEqual(r.root.children[i], expected[i]) {
				equal = false
				break
			}
		}
	}
	if !equal {
		t.Errorf("input: %q\ngot:\n%swant:\n%s",
			tc.Markdown, dumpTree(r.root.children), dumpTree(expected))
	}
}

func TestSMDSuite(t *testing.T) {
	for _, tc := range loadCases(t) {
		t.Run(tc.Title, func(t *testing.T) {
			runCase(t, tc, false)
		})
		t.Run(tc.Title+"; by_char", func(t *testing.T) {
			runCase(t, tc, true)
		})
	}
}

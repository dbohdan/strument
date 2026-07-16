// Layered unit tests per repomap-spec §9: PageRank parity against networkx
// 3.x fixtures, ranker multiplier behavior with injected tags, and
// TreeContext goldens pinned against grep_ast.

package repomap

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ts "github.com/odvcencio/gotreesitter"
)

// Values generated once with networkx 3.x (attic/venv, 2026-07-16) for the
// graph: a->b (2.0 foo), a->b (1.0 bar, parallel), c->b (5.0 foo),
// b->b (0.1 self), e->a (1.5 baz), plus an isolated node d.
func TestPageRankParityNetworkx(t *testing.T) {
	nodes := []string{"a.py", "b.py", "c.py", "d.py", "e.py"}
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n] = i
	}
	out := make([]map[int]float64, len(nodes))
	for i := range out {
		out[i] = map[int]float64{}
	}
	out[idx["a.py"]][idx["b.py"]] = 3.0 // parallel edges pre-summed
	out[idx["c.py"]][idx["b.py"]] = 5.0
	out[idx["b.py"]][idx["b.py"]] = 0.1
	out[idx["e.py"]][idx["a.py"]] = 1.5

	pers := map[string]float64{"a.py": 25.0, "c.py": 25.0}
	got, err := pageRank(nodes, out, pers)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"a.py": 0.07500000000000001, "b.py": 0.85,
		"c.py": 0.07500000000000001, "d.py": 0.0, "e.py": 0.0,
	}
	for n, w := range want {
		if math.Abs(got[n]-w) > 1e-6 {
			t.Errorf("personalized %s = %v, want %v", n, got[n], w)
		}
	}

	got2, err := pageRank(nodes, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	want2 := map[string]float64{
		"a.py": 0.06686758646711716, "b.py": 0.8246986202993244,
		"c.py": 0.03614459774451953, "d.py": 0.03614459774451953,
		"e.py": 0.03614459774451953,
	}
	for n, w := range want2 {
		if math.Abs(got2[n]-w) > 1e-6 {
			t.Errorf("unpersonalized %s = %v, want %v", n, got2[n], w)
		}
	}
}

func TestPageRankZeroPersonalization(t *testing.T) {
	nodes := []string{"a", "b"}
	out := []map[int]float64{{1: 1.0}, {}}
	if _, err := pageRank(nodes, out, map[string]float64{"zzz": 1.0}); err == nil {
		t.Error("want error when personalization has no mass on graph nodes")
	}
}

// rankerHarness builds a RepoMap over real (empty) temp files with injected
// tags, and returns the ranked item list.
func rankerHarness(t *testing.T, fileTags map[string][]Tag, chat []string, mentionedIdents map[string]bool) []MapItem {
	t.Helper()
	dir := t.TempDir()
	abs := map[string]string{}
	var chatAbs, otherAbs []string
	chatSet := map[string]bool{}
	for _, c := range chat {
		chatSet[c] = true
	}
	for rel := range fileTags {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		abs[rel] = p
		if chatSet[rel] {
			chatAbs = append(chatAbs, p)
		} else {
			otherAbs = append(otherAbs, p)
		}
	}
	rm := New(dir)
	rm.Warn = func(format string, args ...any) { t.Logf("warn: "+format, args...) }
	rm.TagsOverride = func(fname, relFname string) []Tag {
		var out []Tag
		for _, tag := range fileTags[relFname] {
			tag.RelFname = relFname
			tag.Fname = fname
			out = append(out, tag)
		}
		return out
	}
	inv := newInvocation()
	return rm.getRankedTags(inv, chatAbs, otherAbs, map[string]bool{}, mentionedIdents)
}

// firstDefNames returns the Names of leading Tag items, in rank order.
func firstDefNames(items []MapItem) []string {
	var out []string
	for _, it := range items {
		if it.Tag != nil {
			out = append(out, it.Tag.Name)
		}
	}
	return out
}

func TestRankerLongSnakeNameBoost(t *testing.T) {
	items := rankerHarness(t, map[string][]Tag{
		"def.py": {
			{Line: 1, Name: "long_snake_name", Kind: Def},
			{Line: 2, Name: "shrt", Kind: Def},
		},
		"ref.py": {
			{Line: 1, Name: "long_snake_name", Kind: Ref},
			{Line: 2, Name: "shrt", Kind: Ref},
		},
	}, nil, nil)
	names := firstDefNames(items)
	if len(names) < 2 || names[0] != "long_snake_name" {
		t.Errorf("long snake_case ident should outrank: %v", names)
	}
}

func TestRankerMentionedIdentBoost(t *testing.T) {
	items := rankerHarness(t, map[string][]Tag{
		"def.py": {
			{Line: 1, Name: "aaa", Kind: Def},
			{Line: 2, Name: "bbb", Kind: Def},
		},
		"ref.py": {
			{Line: 1, Name: "aaa", Kind: Ref},
			{Line: 2, Name: "bbb", Kind: Ref},
		},
	}, nil, map[string]bool{"bbb": true})
	names := firstDefNames(items)
	if len(names) < 2 || names[0] != "bbb" {
		t.Errorf("mentioned ident should outrank: %v", names)
	}
}

func TestRankerPrivatePenalty(t *testing.T) {
	items := rankerHarness(t, map[string][]Tag{
		"def.py": {
			{Line: 1, Name: "_hidden", Kind: Def},
			{Line: 2, Name: "visible", Kind: Def},
		},
		"ref.py": {
			{Line: 1, Name: "_hidden", Kind: Ref},
			{Line: 2, Name: "visible", Kind: Ref},
		},
	}, nil, nil)
	names := firstDefNames(items)
	if len(names) < 2 || names[0] != "visible" {
		t.Errorf("_private ident should be penalized: %v", names)
	}
}

func TestRankerMultiplicitySqrt(t *testing.T) {
	items := rankerHarness(t, map[string][]Tag{
		"def.py": {
			{Line: 1, Name: "hot", Kind: Def},
			{Line: 2, Name: "cold", Kind: Def},
		},
		"ref.py": {
			{Line: 1, Name: "hot", Kind: Ref},
			{Line: 2, Name: "hot", Kind: Ref},
			{Line: 3, Name: "hot", Kind: Ref},
			{Line: 4, Name: "hot", Kind: Ref},
			{Line: 5, Name: "cold", Kind: Ref},
		},
	}, nil, nil)
	names := firstDefNames(items)
	if len(names) < 2 || names[0] != "hot" {
		t.Errorf("higher-multiplicity ident should outrank: %v", names)
	}
}

func TestRankerChatFileBoost(t *testing.T) {
	// chat.py references x; other.py references y; both defined in def.py.
	// The chat referencer's 50x multiplier should push x first.
	items := rankerHarness(t, map[string][]Tag{
		"def.py": {
			{Line: 1, Name: "xxx", Kind: Def},
			{Line: 2, Name: "yyy", Kind: Def},
		},
		"chat.py":  {{Line: 1, Name: "xxx", Kind: Ref}},
		"other.py": {{Line: 1, Name: "yyy", Kind: Ref}},
	}, []string{"chat.py"}, nil)
	names := firstDefNames(items)
	if len(names) < 2 || names[0] != "xxx" {
		t.Errorf("chat-referenced ident should outrank: %v", names)
	}
}

func TestRankerChatFilesExcludedButPresentAsBareNodes(t *testing.T) {
	items := rankerHarness(t, map[string][]Tag{
		"chat.py": {{Line: 1, Name: "in_chat_def", Kind: Def}},
		"ref.py":  {{Line: 1, Name: "in_chat_def", Kind: Ref}},
	}, []string{"chat.py"}, nil)
	for _, it := range items {
		if it.Tag != nil && it.RelFname == "chat.py" {
			t.Errorf("chat file def tags must be skipped: %+v", it)
		}
	}
	// The chat file still appears as a bare node (skipped later in toTree
	// but counted for truncation, §3.6).
	found := false
	for _, it := range items {
		if it.Tag == nil && it.RelFname == "chat.py" {
			found = true
		}
	}
	if !found {
		t.Error("chat file should appear as a bare node")
	}
}

// TreeContext goldens pinned against grep_ast (attic/venv, 2026-07-16) with
// the repo-map configuration.
const treeCtxSample = `import os

class Outer:
    class Inner:
        def deep_method(self):
            pass

    def method_one(self,
                   arg1,
                   arg2):
        if arg1:
            return arg2
        return None

def top_level():
    x = 1

    y = 2
    return x + y

CONST = 42
`

func buildPyTreeContext(t *testing.T, code string) *TreeContext {
	t.Helper()
	entry, err := langFor("python")
	if err != nil || entry == nil {
		t.Fatal("python grammar unavailable")
	}
	parser := ts.NewParser(entry.language)
	tree, err := parser.Parse([]byte(code))
	if err != nil {
		t.Fatal(err)
	}
	return NewTreeContext(code, tree.RootNode())
}

func TestTreeContextGoldens(t *testing.T) {
	ctx := buildPyTreeContext(t, treeCtxSample)
	cases := []struct {
		lois []int
		want string
	}{
		// Nested class headers via the enclosing block's header.
		{[]int{4}, "⋮\n│class Outer:\n│    class Inner:\n│        def deep_method(self):\n⋮\n"},
		// Multiline signature: the parameters node is the smaller header
		// candidate, so the def shows two lines.
		{[]int{7}, "⋮\n│class Outer:\n│    class Inner:\n│        def deep_method(self):\n⋮\n│    def method_one(self,\n│                   arg1,\n⋮\n"},
		// Blank-line pickup after a shown content line.
		{[]int{15}, "⋮\n│def top_level():\n│    x = 1\n│\n⋮\n"},
		// Multiple lois; trailing loi on the last content line.
		{[]int{4, 7, 15, 20}, "⋮\n│class Outer:\n│    class Inner:\n│        def deep_method(self):\n⋮\n│    def method_one(self,\n│                   arg1,\n⋮\n│def top_level():\n│    x = 1\n│\n⋮\n│CONST = 42\n"},
	}
	for _, c := range cases {
		ctx.SetLinesOfInterest(c.lois)
		ctx.AddContext()
		got := ctx.Format()
		if got != c.want {
			t.Errorf("lois %v:\nwant:\n%s\ngot:\n%s", c.lois, c.want, got)
		}
	}
}

func TestTreeContextLine0NotForced(t *testing.T) {
	// show_top_of_file_parent_scope=false: a loi deep in the file must not
	// force the module header (line 0) in; the leading elision marker
	// appears instead.
	ctx := buildPyTreeContext(t, treeCtxSample)
	ctx.SetLinesOfInterest([]int{20})
	ctx.AddContext()
	got := ctx.Format()
	if !strings.HasPrefix(got, "⋮\n") {
		t.Errorf("expected leading elision, got:\n%s", got)
	}
	if strings.Contains(got, "import os") {
		t.Errorf("line 0 should not be forced in:\n%s", got)
	}
}

func TestImportantFiles(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"go.mod", true},
		{".github/workflows/ci.yml", true},         // the single glob case
		{".github/workflows/nested/ci.yml", false}, // not direct child
		{".github/dependabot.yml", true},
		{"src/README.md", false}, // root-relative exact match only
		{"main.go", false},
		{".circleci/config.yml", true},
	}
	for _, c := range cases {
		if got := isImportant(c.path); got != c.want {
			t.Errorf("isImportant(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// Budget behavior: a tiny budget yields a truncated but non-empty map; a
// huge budget includes everything (soft target, §4.2).
func TestBudgetTruncation(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		files[name+".py"] = "def func_in_" + name + "():\n    pass\n"
	}
	other := writeFiles(t, dir, files)

	rm := testMap(t, dir)
	rm.MapTokens = 5000
	full := rm.GetRepoMap(nil, other, nil, nil)

	rm2 := testMap(t, dir)
	rm2.MapTokens = 32
	small := rm2.GetRepoMap(nil, other, nil, nil)

	if len(small) == 0 {
		t.Fatal("small budget produced an empty map")
	}
	if len(small) >= len(full) {
		t.Errorf("truncation did not shrink the map: small=%d full=%d", len(small), len(full))
	}
}

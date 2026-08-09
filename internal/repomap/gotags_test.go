package repomap

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The go/parser extractor replaces tree-sitter for Go everywhere, including the
// repo map, so "does the map change" has to be a measurement rather than a
// hope. These tests are that measurement. If definition parity ever stops being
// exact, the extractor is wrong — not the test.

// sites reduces tags of one kind to "line:name", which is everything the two
// extractors can be held to. Enclosing is excluded because tree-sitter never
// fills it in — the one place the go/parser path is allowed to know more.
func sites(tags []Tag, want Kind) []string {
	var out []string
	for _, t := range tags {
		if t.Kind != want {
			continue
		}
		out = append(out, fmt.Sprintf("%d:%s", t.Line+1, t.Name))
	}
	slices.Sort(out)
	return out
}

// treeSitterTags extracts with the grammar, whatever the dispatch in
// invocation.tags currently does.
func treeSitterTags(t *testing.T, rm *RepoMap, abs, rel string) []Tag {
	t.Helper()
	inv := newInvocation()
	return extractTags(rel, abs, inv.parse(rm, abs, rel))
}

// diffCounts reports what is in a and not b, and vice versa, as multisets.
func diffCounts(a, b []string) (onlyA, onlyB []string) {
	count := map[string]int{}
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
	}
	for s, n := range count {
		for range n {
			onlyA = append(onlyA, s)
		}
		for range -n {
			onlyB = append(onlyB, s)
		}
	}
	slices.Sort(onlyA)
	slices.Sort(onlyB)
	return onlyA, onlyB
}

// goParityFixture covers the constructs where the two extractors could
// plausibly disagree. Every line here was chosen because it exercises a rule in
// queries/go-tags.scm, a quirk of one, or a Go form the query has no rule for
// at all.
const goParityFixture = `package fixture

import "fmt"

import (
	"os"
	"strings"
)

type Plain int

type Alias = string

type Pair struct {
	Key   string
	Value []byte
}

type Speaker interface {
	Speak(to Pair) (string, error)
}

type List[T comparable] struct {
	items []T
	next  *List[T]
	fn    func(a T, b ...string) map[string][]T
}

type Embedder struct {
	List[int]
	os.File
	*Pair
	Name string
}

type Constrained[T ~int | ~string] struct{ v T }

var topVar = 1
var topTyped List[int]
var topMulti, topOther = 2, 3
var _ Speaker = (*Impl)(nil)

const topConst = 4

const (
	blockA = iota
	blockB
)

type Impl struct{}

func (i Impl) Speak(to Pair) (string, error) {
	return strings.ToUpper(to.Key), nil
}

func (l *List[T]) Push(v T) {
	l.items = append(l.items, v)
}

func plain(a Pair, b ...string) (out map[string][]int, err error) {
	var local int
	const localConst = 5
	short := 6
	conv := []byte("hi")
	arr := [4]Pair{}
	lit := List[int]{}
	fn := func(x Plain) Plain { return x }
	ch := make(chan List[int], 2)

	switch v := any(a).(type) {
	case *List[int]:
		_ = v
	case fmt.Stringer:
		_ = v
	case nil:
	}

	if s, ok := any(a).(fmt.Stringer); ok {
		_ = s
	}

	switch local {
	case 1:
		fmt.Println("one")
	}

	fmt.Println(local, localConst, short, conv, arr, lit, fn, ch, os.Args)
	return nil, os.ErrNotExist
}
`

func TestGoTagParityOnConstructs(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(abs, []byte(goParityFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	rm := testMap(t, dir)

	ts := treeSitterTags(t, rm, abs, "fixture.go")
	gp := goTags("fixture.go", abs)

	for _, kind := range []struct {
		k    Kind
		name string
	}{{Def, "definition"}, {Ref, "reference"}} {
		want := sites(ts, kind.k)
		got := sites(gp, kind.k)
		onlyTS, onlyGo := diffCounts(want, got)
		if len(onlyTS) > 0 || len(onlyGo) > 0 {
			t.Errorf("%s tags differ (%d from tree-sitter, %d from go/parser)\n"+
				"  only tree-sitter: %v\n  only go/parser:   %v",
				kind.name, len(want), len(got), onlyTS, onlyGo)
		}
	}
}

// TestGoTagParityOverThisRepo is the in-distribution half: every Go file the
// project has, which is also the corpus every measurement behind this change
// was taken on.
func TestGoTagParityOverThisRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("the tree-sitter half of this comparison costs about three seconds")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "reference", "attic", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 50 {
		t.Fatalf("expected the project's own Go files, found %d", len(files))
	}

	rm := testMap(t, root)
	var defsTotal, refsTotal, refsAgreed int
	for _, abs := range files {
		rel := rm.relFname(abs)
		ts := treeSitterTags(t, rm, abs, rel)
		gp := goTags(rel, abs)

		wantDefs, gotDefs := sites(ts, Def), sites(gp, Def)
		onlyTS, onlyGo := diffCounts(wantDefs, gotDefs)
		if len(onlyTS) > 0 || len(onlyGo) > 0 {
			t.Errorf("%s: definition tags differ\n  only tree-sitter: %v\n  only go/parser:   %v",
				rel, onlyTS, onlyGo)
		}
		defsTotal += len(wantDefs)

		wantRefs, gotRefs := sites(ts, Ref), sites(gp, Ref)
		missing, extra := diffCounts(wantRefs, gotRefs)
		refsTotal += len(wantRefs)
		refsAgreed += len(wantRefs) - len(missing)
		if len(missing) > 0 || len(extra) > 0 {
			t.Logf("%s: %d refs, %d missing, %d extra\n  missing: %v\n  extra:   %v",
				rel, len(wantRefs), len(missing), len(extra), first(missing, 8), first(extra, 8))
		}
	}

	t.Logf("%d files, %d definition tags, %d reference tags", len(files), defsTotal, refsTotal)
	agreement := 100 * float64(refsAgreed) / float64(refsTotal)
	t.Logf("reference agreement: %.2f%%", agreement)

	// Measured at 99.97% when this was written — 5 tags out of 15,360, with no
	// extras in either direction beyond them, and definition parity exact.
	//
	// The residue is one thing and it is not fixable here: tree-sitter resolves
	// `X[Y]` in expression position sometimes as an index and sometimes as a
	// generic type, from parse state rather than from meaning, so
	// `definitions[d.fname][d.ident]` comes back as three type identifiers while
	// `list.buffer[pos]` comes back as none. go/parser has an unambiguous AST
	// and cannot reproduce that, which is most of the reason to prefer it.
	// Reproducing a parser's conflict resolution is not a contract worth
	// keeping, and five edges do not move a PageRank over fifteen thousand.
	//
	// A floor rather than an equality, so that a Go release adding syntax shows
	// up as a number to look at instead of a red build. A drop is still a
	// regression to investigate, not to bless by lowering this.
	const minRefAgreement = 99.9
	if agreement < minRefAgreement {
		t.Errorf("reference agreement %.2f%% is below the pinned floor of %.1f%%", agreement, minRefAgreement)
	}
}

func first(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(slices.Clone(s[:n]), "…")
}

// TestGoRepoMapUnchanged is the same question asked in the user's units: the
// rendered map for a Go tree must be identical under either extractor.
func TestGoRepoMapUnchanged(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"fixture.go": goParityFixture,
		"user.go": `package fixture

import "fmt"

func useAll() {
	p := Pair{Key: "k"}
	var l List[int]
	l.Push(1)
	i := Impl{}
	fmt.Println(i.Speak(p))
	plain(p, "x")
}
`,
	}
	paths := writeFiles(t, dir, files)

	tsMap := renderWith(t, dir, paths, false)
	goMap := renderWith(t, dir, paths, true)
	if tsMap != goMap {
		t.Errorf("the rendered map differs between extractors\n--- tree-sitter ---\n%s\n--- go/parser ---\n%s",
			tsMap, goMap)
	}
	if strings.TrimSpace(tsMap) == "" {
		t.Error("the fixture produced an empty map, so this compares nothing")
	}
}

// renderWith builds the map for paths, forcing one extractor or the other
// through TagsOverride.
func renderWith(t *testing.T, dir string, paths []string, useGo bool) string {
	t.Helper()
	rm := testMap(t, dir)
	base := testMap(t, dir) // a separate RepoMap so the override cannot recurse
	rm.TagsOverride = func(fname, relFname string) []Tag {
		if useGo {
			return goTags(relFname, fname)
		}
		return treeSitterTags(t, base, fname, relFname)
	}
	return strings.TrimSpace(rm.GetRepoMap(nil, paths, nil, nil))
}

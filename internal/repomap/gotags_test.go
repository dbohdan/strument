package repomap

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
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

// namedStructFields lists the "line:Name" sites of every field of a struct
// declared with a name, computed straight from go/ast rather than from the
// tagger under test.
//
// It exists because the Go fast path deliberately diverges from the
// tree-sitter query in exactly one way: the query has no rule for field names,
// and symbol needs them — half the identifiers models actually looked up in
// live sessions and missed were struct fields. Subtracting an independently
// computed set keeps the parity assertion real: any *other* drift between the
// two extractors still fails, which is the whole job of these tests.
func namedStructFields(t *testing.T, abs string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil // an unparsable file has no tags from either side
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Assign.IsValid() {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, fld := range st.Fields.List {
			for _, name := range fld.Names {
				out = append(out, fmt.Sprintf("%d:%s", fset.Position(name.Pos()).Line, name.Name))
			}
		}
		return true
	})
	slices.Sort(out)
	return out
}

// withoutFieldTags drops the named-struct field definitions the Go extractor
// adds on purpose, so a whole-tag-set comparison against tree-sitter still
// holds everything else to exact parity.
func withoutFieldTags(t *testing.T, tags []Tag, absByRel map[string]string) []Tag {
	t.Helper()
	exempt := map[string]bool{}
	for rel, abs := range absByRel {
		for _, site := range namedStructFields(t, abs) {
			exempt[rel+":"+site] = true
		}
	}
	out := tags[:0:0]
	for _, tag := range tags {
		if tag.Kind == Def && exempt[fmt.Sprintf("%s:%d:%s", tag.RelFname, tag.Line+1, tag.Name)] {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// withoutSites removes one occurrence of each exempt site from got.
func withoutSites(got, exempt []string) []string {
	drop := map[string]int{}
	for _, s := range exempt {
		drop[s]++
	}
	out := got[:0:0]
	for _, s := range got {
		if drop[s] > 0 {
			drop[s]--
			continue
		}
		out = append(out, s)
	}
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
		if kind.k == Def {
			got = withoutSites(got, namedStructFields(t, abs))
		}
		onlyTS, onlyGo := diffCounts(want, got)
		if len(onlyTS) > 0 || len(onlyGo) > 0 {
			t.Errorf("%s tags differ (%d from tree-sitter, %d from go/parser)\n"+
				"  only tree-sitter: %v\n  only go/parser:   %v",
				kind.name, len(want), len(got), onlyTS, onlyGo)
		}
	}
}

// newMakeTypeArg reports whether name occurs on line as the type argument of a
// new(...) or make(...) call. It is the predicate for the second known cause of
// parity divergence, and it is deliberately narrow: the name must follow the
// opening paren directly, allowing only a package qualifier or a pointer star,
// so the allowance covers `new(Config)`, `new(bytes.Buffer)` and
// `make(http.Header)` and cannot quietly absorb a name that merely appears
// somewhere on a line that happens to call new or make.
func newMakeTypeArg(line, name string) bool {
	re, err := regexp.Compile(`\b(?:new|make)\(\s*\*?(?:[\p{L}_][\p{L}\p{N}_]*\.)?` +
		regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return false
	}
	return re.MatchString(line)
}

// TestNewMakeTypeArgIsNarrow pins the allowance above to the shape it is meant
// to excuse. An allowance that matched a name anywhere on a line calling new or
// make would let the parity test absorb real drift, which is the one thing it
// exists to catch.
func TestNewMakeTypeArgIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		line, name string
		want       bool
	}{
		{"buf := new(bytes.Buffer)", "Buffer", true},
		{"result := new(Config)", "Config", true},
		{"\tHeader:     make(http.Header),", "Header", true},
		{"p := new(*Pair)", "Pair", true},

		// The name is on a line that calls new, but is not its type argument.
		{"foo := new(Config); bar(Other)", "Other", false},
		// Not directly after the paren.
		{"ch := make(chan Element)", "Element", false},
		// new/make must be the whole word, not a suffix or prefix.
		{"x := renew(Config)", "Config", false},
		{"x := newThing(Config)", "Config", false},
		// A different name on the line entirely.
		{"result := new(Config)", "Result", false},
	} {
		if got := newMakeTypeArg(tc.line, tc.name); got != tc.want {
			t.Errorf("newMakeTypeArg(%q, %q) = %v, want %v", tc.line, tc.name, got, tc.want)
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

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	root, err := filepath.Abs(filepath.Join(filename, "..", ".."))
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
	var defsTotal, refsTotal int
	var byBrackets, byNewMake, unexplained int
	for _, abs := range files {
		rel := rm.relFname(abs)
		ts := treeSitterTags(t, rm, abs, rel)
		gp := goTags(rel, abs)

		wantDefs, gotDefs := sites(ts, Def), sites(gp, Def)
		gotDefs = withoutSites(gotDefs, namedStructFields(t, abs))
		onlyTS, onlyGo := diffCounts(wantDefs, gotDefs)
		if len(onlyTS) > 0 || len(onlyGo) > 0 {
			t.Errorf("%s: definition tags differ\n  only tree-sitter: %v\n  only go/parser:   %v",
				rel, onlyTS, onlyGo)
		}
		defsTotal += len(wantDefs)

		wantRefs, gotRefs := sites(ts, Ref), sites(gp, Ref)
		missing, extra := diffCounts(wantRefs, gotRefs)
		refsTotal += len(wantRefs)

		// go/parser must never invent a reference the grammar does not have.
		// Divergence is allowed in exactly one direction, and only for the
		// causes characterized below.
		if len(extra) > 0 {
			t.Errorf("%s: go/parser reported references tree-sitter does not: %v", rel, extra)
		}

		src, err := os.ReadFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		srcLines := strings.Split(string(src), "\n")
		for _, m := range missing {
			num, name, _ := strings.Cut(m, ":")
			n, convErr := strconv.Atoi(num)
			if convErr != nil || n < 1 || n > len(srcLines) {
				t.Errorf("%s: unaccountable missing reference %q", rel, m)
				continue
			}
			// Two known causes, each characterized rather than pinned to a line
			// number, so ordinary edits to these files cannot break the test
			// while a divergence of any *other* kind fails immediately.
			line := srcLines[n-1]
			switch {
			// Brackets: tree-sitter resolves `X[Y]` as a generic type in some
			// parse states and as an index in others, from state rather than
			// from meaning — so definitions[d.fname][d.ident] comes back as
			// three type identifiers while list.buffer[pos] comes back as none.
			case strings.Contains(line, "["):
				byBrackets++
				t.Logf("%s:%d %s — tree-sitter read the brackets as a generic type: %s",
					rel, n, name, strings.TrimSpace(line))

			// new/make: their first argument is a type, and the grammar stopped
			// reporting it as an identifier reference in v0.44.0 (it worsened
			// again at v0.51.0, which also dropped the qualified form). This is
			// a fact about the grammar we do not use for Go — .go files go to
			// goTags — so it costs nothing but this comparison.
			case newMakeTypeArg(line, name):
				byNewMake++
				t.Logf("%s:%d %s — the type argument of new/make, which the grammar does not report: %s",
					rel, n, name, strings.TrimSpace(line))

			default:
				unexplained++
				t.Errorf("%s:%d: reference %q is missing for neither known cause "+
					"— no bracket on the line, and not a new/make type argument:\n\t%s",
					rel, n, name, strings.TrimSpace(line))
			}
		}
	}

	diverged := byBrackets + byNewMake + unexplained
	t.Logf("%d files, %d definition tags (exact parity), %d reference tags",
		len(files), defsTotal, refsTotal)
	// Each cause counted separately, and unexplained counted at all: the
	// previous version of this line passed accountedFor for both the "diverge"
	// and the "accounted for" slot and hardcoded "100% explained", so it
	// reported everything explained in the same run that five unexplained
	// divergences failed the test. A summary that cannot say the bad thing is
	// not a summary.
	t.Logf("%d references diverge: %d brackets, %d new/make, %d unexplained — %.2f%% identical",
		diverged, byBrackets, byNewMake, unexplained,
		100*float64(refsTotal-diverged)/float64(refsTotal))
}

// TestGoTagsSurviveAMidEditFile is the reservation this extractor was weighed
// against: a call-graph feature that goes blank while the user is typing is
// worse than none. A type-resolved graph does exactly that — it needs the
// package to type-check, which mid-edit it does not — and this is the reason
// the syntactic answer was chosen instead.
//
// The promise is precise and partial: **every definition ahead of the break is
// still found**. What comes after it is not promised at all, and the count can
// even come out higher than the intact file's — error recovery closes a
// function early and reparses the rest of its body as top-level declarations.
// Those are transient and self-correcting, since the file is retagged the
// moment it changes again; losing the declarations above the cursor would not
// be.
func TestGoTagsSurviveAMidEditFile(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(abs, []byte(goParityFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(goParityFixture, "\n")
	mid := len(lines) / 2

	// The definitions above the break, which is what must survive. Breaks are
	// inserted at mid, so these keep their line numbers in every variant.
	var above []string
	for _, s := range sites(goTags("fixture.go", abs), Def) {
		num, _, _ := strings.Cut(s, ":")
		if n, err := strconv.Atoi(num); err == nil && n <= mid {
			above = append(above, s)
		}
	}
	if len(above) < 5 {
		t.Fatalf("only %d definitions above the break; this measures nothing", len(above))
	}

	for _, tc := range []struct {
		what   string
		broken string
	}{
		{"truncated mid-file", strings.Join(lines[:mid], "\n")},
		{"half-typed statement", strings.Join(slices.Concat(
			lines[:mid], []string{"\tif x := foo("}, lines[mid:]), "\n")},
		{"stray closing braces", strings.Join(slices.Concat(
			lines[:mid], []string{"}}}"}, lines[mid:]), "\n")},
		{"unterminated string", strings.Join(slices.Concat(
			lines[:mid], []string{`	s := "oops`}, lines[mid:]), "\n")},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if err := os.WriteFile(abs, []byte(tc.broken), 0o644); err != nil {
				t.Fatal(err)
			}
			got := sites(goTags("fixture.go", abs), Def)
			if len(got) == 0 {
				t.Fatal("a broken file yielded nothing; the whole file was lost, not its tail")
			}
			lost, _ := diffCounts(above, got)
			if len(lost) > 0 {
				t.Errorf("definitions above the break were lost: %v", lost)
			}
			t.Logf("%d definitions total, all %d above the break intact", len(got), len(above))
		})
	}
}

// TestGoTagsDropEnclosingBelowABreak pins a bug a live pass found and the unit
// tests did not, because they asked the wrong question.
//
// They checked that the definitions *above* a break survive. They never checked
// what is claimed *below* one. With internal/coder/toolobserve.go broken at its
// midpoint, a symbol lookup found all seven call sites — and attributed four of
// runLS's and runChecks's to runGlob, because recovery swallows the rest of the
// file into whichever function was open at the break.
//
// That is exactly the failure the annotation was designed to rule out: not a
// gap, but a confident wrong name pointing at real code. Below the first parse
// error there is no enclosing name at all.
func TestGoTagsDropEnclosingBelowABreak(t *testing.T) {
	const src = `package fixture

func alpha() { helper() }

func beta() { helper() }

func gamma() { helper() }

func helper() {}
`
	dir := t.TempDir()
	abs := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(abs, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	enclosingOf := func(want string) map[int]string {
		out := map[int]string{}
		for _, tag := range goTags("fixture.go", abs) {
			if tag.Name == want && tag.Kind == Ref {
				out[tag.Line+1] = tag.Enclosing
			}
		}
		return out
	}

	intact := enclosingOf("helper")
	for line, want := range map[int]string{3: "alpha", 5: "beta", 7: "gamma"} {
		if intact[line] != want {
			t.Fatalf("intact: line %d attributed to %q, want %q", line, intact[line], want)
		}
	}

	// Break inside alpha, above beta and gamma.
	lines := strings.Split(src, "\n")
	lines[2] = "func alpha() { helper(); if x := ( }"
	if err := os.WriteFile(abs, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	broken := enclosingOf("helper")
	if len(broken) == 0 {
		t.Fatal("a broken file reported no references at all")
	}
	for line, got := range broken {
		if line >= 3 && got != "" {
			t.Errorf("line %d below the break claims to be in %q; below a parse "+
				"error there is nothing trustworthy to say", line, got)
		}
	}
}

// TestGoTagsIsolateABrokenFile: one file mid-edit must not take the rest of the
// project's answers with it.
func TestGoTagsIsolateABrokenFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	bad := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(good, []byte(goParityFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("package fixture\n\nfunc broken() { if x := ("), 0o644); err != nil {
		t.Fatal(err)
	}

	rm := testMap(t, dir)
	var fromGood int
	for _, tag := range rm.Tags([]string{good, bad}) {
		if tag.RelFname == "good.go" {
			fromGood++
		}
	}
	if fromGood == 0 {
		t.Error("a broken file in the workspace silenced an intact one")
	}
}

// TestGoTagsUnchangedAcrossAFileSet asks the parity question over a whole
// directory rather than one file: every tag either extractor reports for a Go
// tree must match, in order.
//
// This was a rendered-map comparison when the map existed — the same question
// in the user's units. The map is gone, so the user's units are Tags, which is
// what both surviving consumers (the symbol tool and /symbol) read. Comparing
// them directly is the same gate with the proxy removed.
func TestGoTagsUnchangedAcrossAFileSet(t *testing.T) {
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

	tsTags := tagsWith(t, dir, paths, false)
	absByRel := map[string]string{}
	for rel := range files {
		absByRel[rel] = filepath.Join(dir, rel)
	}
	goTagSet := withoutFieldTags(t, tagsWith(t, dir, paths, true), absByRel)
	if len(tsTags) == 0 {
		t.Fatal("the fixture produced no tags, so this compares nothing")
	}
	if len(tsTags) != len(goTagSet) {
		t.Fatalf("tag counts differ: tree-sitter %d, go/parser %d", len(tsTags), len(goTagSet))
	}
	// Enclosing is deliberately excluded. go/parser knows the enclosing
	// function exactly and fills it in; tree-sitter cannot say and leaves it
	// empty, which is the asymmetry Stage 7c chose on purpose — exact or absent,
	// never a guess. The rendered map this test replaced never displayed the
	// field, so comparing whole structs would fail on the feature.
	key := func(t Tag) [4]any { return [4]any{t.RelFname, t.Line, t.Name, t.Kind} }
	for i := range tsTags {
		if key(tsTags[i]) != key(goTagSet[i]) {
			t.Errorf("tag %d differs:\n tree-sitter %+v\n go/parser   %+v", i, tsTags[i], goTagSet[i])
		}
	}
}

// tagsWith collects the tags for paths, forcing one extractor or the other
// through TagsOverride, in a stable order.
func tagsWith(t *testing.T, dir string, paths []string, useGo bool) []Tag {
	t.Helper()
	rm := testMap(t, dir)
	base := testMap(t, dir) // a separate RepoMap so the override cannot recurse
	rm.TagsOverride = func(fname, relFname string) []Tag {
		if useGo {
			return goTags(relFname, fname)
		}
		return treeSitterTags(t, base, fname, relFname)
	}
	tags := rm.Tags(paths)
	slices.SortFunc(tags, func(a, b Tag) int {
		switch {
		case lessTag(a, b):
			return -1
		case lessTag(b, a):
			return 1
		}
		return 0
	})
	return tags
}

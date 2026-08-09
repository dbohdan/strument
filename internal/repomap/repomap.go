package repomap

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
)

// RepoMap generates the ranked repository map. Tags are cached across calls
// (tagCache below); compiled queries are process-lifetime; parse trees, sources,
// and TreeContexts are memoized only within one call, because they are the large
// objects and the tags are what gets asked for again.
type RepoMap struct {
	Root              string
	MapTokens         int     // token budget target; <=0 disables the map
	MapMulNoFiles     float64 // widening multiplier with no chat files
	MaxContextWindow  int     // 0 => unknown; disables widening
	RepoContentPrefix string  // optional framing prefix; may contain {other}
	Warn              func(format string, args ...any)

	// TagsOverride, when non-nil, replaces tree-sitter tag extraction; a
	// test seam for exercising the ranker with injected tags.
	TagsOverride func(fname, relFname string) []Tag

	warnedFiles map[string]bool

	// tagMu guards tagCache. Nothing in the REPL currently tags concurrently —
	// it is single-threaded, and its one goroutine watches signals — but a cache
	// is exactly the field a later caller reaches for without checking, and an
	// uncontended mutex costs nothing next to a parse.
	tagMu    sync.Mutex
	tagCache map[string]tagCacheEntry
}

// tagCacheEntry is one file's tags and the version of the file they came from.
//
// mtime alone is the usual key and is not quite enough here: this harness edits
// files itself, several times a turn, and two writes within one filesystem
// timestamp tick are ordinary rather than exotic. Size catches most of what
// mtime misses, for one extra field.
type tagCacheEntry struct {
	stamp fileStamp
	tags  []Tag
}

// fileStamp identifies a version of a file cheaply.
type fileStamp struct {
	modTime time.Time
	size    int64
}

// statStamp reads fname's stamp. ok is false when the file cannot be stat'd,
// and then nothing is cached: a file that is not there cannot be keyed, and
// guessing would cache an answer no later change could invalidate.
func statStamp(fname string) (stamp fileStamp, ok bool) {
	fi, err := os.Stat(fname)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{modTime: fi.ModTime(), size: fi.Size()}, true
}

// cachedTags returns fname's tags when they were extracted from the version of
// the file that stamp describes.
func (rm *RepoMap) cachedTags(fname string, stamp fileStamp) ([]Tag, bool) {
	rm.tagMu.Lock()
	defer rm.tagMu.Unlock()
	entry, ok := rm.tagCache[fname]
	if !ok || entry.stamp != stamp {
		return nil, false
	}
	return entry.tags, true
}

// storeTags records tags for fname under stamp.
func (rm *RepoMap) storeTags(fname string, stamp fileStamp, tags []Tag) {
	rm.tagMu.Lock()
	defer rm.tagMu.Unlock()
	if rm.tagCache == nil {
		rm.tagCache = map[string]tagCacheEntry{}
	}
	rm.tagCache[fname] = tagCacheEntry{stamp: stamp, tags: tags}
}

// New builds a RepoMap with aider's defaults (map_tokens=1024,
// map_mul_no_files=8).
func New(root string) *RepoMap {
	return &RepoMap{
		Root:          root,
		MapTokens:     1024,
		MapMulNoFiles: 8,
		warnedFiles:   map[string]bool{},
	}
}

func (rm *RepoMap) warnf(format string, args ...any) {
	if rm.Warn != nil {
		rm.Warn(format, args...)
	} else {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

// relFname is the centralized path canonicalization:
// root-relative, forward-slashed; cross-mount falls back to the absolute
// path.
func (rm *RepoMap) relFname(fname string) string {
	rel, err := filepath.Rel(rm.Root, fname)
	if err != nil {
		return fname
	}
	return filepath.ToSlash(rel)
}

// invocation carries the per-call memoization: parse trees, tags,
// sources, and built TreeContexts, discarded when the call returns.
type invocation struct {
	parsed   map[string]*parsedFile
	fileTags map[string][]Tag
	treeCtx  map[string]*TreeContext
	rendered map[string]string
}

func newInvocation() *invocation {
	return &invocation{
		parsed:   map[string]*parsedFile{},
		fileTags: map[string][]Tag{},
		treeCtx:  map[string]*TreeContext{},
		rendered: map[string]string{},
	}
}

// parse returns the memoized parse of fname, or nil when the file is
// unsupported/unreadable/unparseable.
func (inv *invocation) parse(rm *RepoMap, fname, relFname string) *parsedFile {
	if pf, ok := inv.parsed[relFname]; ok {
		return pf
	}
	pf := func() *parsedFile {
		lang := filenameToLang(fname)
		if lang == "" {
			return nil
		}
		entry, err := langFor(lang)
		if err != nil {
			rm.warnf("Skipping file %s: %v", fname, err)
			return nil
		}
		if entry == nil {
			return nil
		}
		src, err := os.ReadFile(fname)
		if err != nil || len(src) == 0 {
			return nil
		}
		if !utf8.Valid(src) {
			if !rm.warnedFiles[fname] {
				rm.warnf("Repo-map skipping non-UTF-8 file %s", fname)
				rm.warnedFiles[fname] = true
			}
			return nil
		}
		parser := ts.NewParser(entry.language)
		tree, err := parser.Parse(src)
		if err != nil || tree == nil {
			return nil
		}
		return &parsedFile{src: src, tree: tree, lang: entry}
	}()
	inv.parsed[relFname] = pf
	return pf
}

// tags returns fname's tags, from this call's memo, then the cross-call cache,
// then the extractor.
//
// TagsOverride deliberately skips the cache. It is the seam that injects tags
// for the ranker's tests, and caching around an injected answer would serve a
// stale one to the next call — a failure that would present as a ranker bug,
// which is a bad way to spend an afternoon.
func (inv *invocation) tags(rm *RepoMap, fname, relFname string) []Tag {
	if t, ok := inv.fileTags[relFname]; ok {
		return t
	}
	if rm.TagsOverride != nil {
		t := rm.TagsOverride(fname, relFname)
		inv.fileTags[relFname] = t
		return t
	}

	before, ok := statStamp(fname)
	if ok {
		if t, hit := rm.cachedTags(fname, before); hit {
			inv.fileTags[relFname] = t
			return t
		}
	}

	// Go goes to go/parser, which is exact and about seventy times faster; the
	// dispatch is here rather than inside extractTags so the tree-sitter parse
	// never happens at all. Every other language keeps the grammar. See
	// gotags.go, and parse.go for the same split in ParseStatus.
	var t []Tag
	if strings.HasSuffix(fname, ".go") {
		t = goTags(relFname, fname)
	} else {
		t = extractTags(relFname, fname, inv.parse(rm, fname, relFname))
	}

	// Stat again and store only if the file did not move under us. Otherwise a
	// write landing during the parse would be recorded under the pre-write
	// stamp, and the stale tags would survive every later lookup until the file
	// happened to change again.
	if after, ok2 := statStamp(fname); ok && ok2 && after == before {
		rm.storeTags(fname, before, t)
	}
	inv.fileTags[relFname] = t
	return t
}

// tokenEstimate is the cheap estimator: code points/4.
func tokenEstimate(text string) float64 {
	return float64(utf8.RuneCountInString(text)) / 4.0
}

// GetRepoMap ports get_repo_map: budget widening when the chat is empty,
// ranking, fitting, and the framing prefix. Returns "" when the map is
// disabled or empty.
func (rm *RepoMap) GetRepoMap(chatFiles, otherFiles []string, mentionedFnames, mentionedIdents map[string]bool) string {
	if rm.MapTokens <= 0 || len(otherFiles) == 0 {
		return ""
	}
	if rm.warnedFiles == nil {
		rm.warnedFiles = map[string]bool{}
	}

	maxMapTokens := rm.MapTokens

	// With no chat files, widen — only when the context window is known.
	const padding = 4096
	target := 0
	if maxMapTokens > 0 && rm.MaxContextWindow > 0 {
		target = min(int(float64(maxMapTokens)*rm.MapMulNoFiles), rm.MaxContextWindow-padding)
	}
	if len(chatFiles) == 0 && rm.MaxContextWindow > 0 && target > 0 {
		maxMapTokens = target
	}

	filesListing := rm.rankedTagsMap(chatFiles, otherFiles, maxMapTokens, mentionedFnames, mentionedIdents)
	if filesListing == "" {
		return ""
	}

	other := ""
	if len(chatFiles) > 0 {
		other = "other "
	}
	repoContent := strings.ReplaceAll(rm.RepoContentPrefix, "{other}", other)
	return repoContent + filesListing
}

// rankedTagsMap ports get_ranked_tags_map_uncached: important-files prepend
// and the binary search over the ranked prefix.
func (rm *RepoMap) rankedTagsMap(chatFnames, otherFnames []string, maxMapTokens int, mentionedFnames, mentionedIdents map[string]bool) string {
	if mentionedFnames == nil {
		mentionedFnames = map[string]bool{}
	}
	if mentionedIdents == nil {
		mentionedIdents = map[string]bool{}
	}

	inv := newInvocation()

	rankedTags := rm.getRankedTags(inv, chatFnames, otherFnames, mentionedFnames, mentionedIdents)

	// Important files among other_files not already ranked, prepended bare
	// so they survive truncation.
	otherRel := map[string]bool{}
	for _, f := range otherFnames {
		otherRel[rm.relFname(f)] = true
	}
	rankedFnames := map[string]bool{}
	for _, item := range rankedTags {
		rankedFnames[item.RelFname] = true
	}
	var special []MapItem
	for _, fn := range filterImportantFiles(slices.Sorted(maps.Keys(otherRel))) {
		if !rankedFnames[fn] {
			special = append(special, MapItem{RelFname: fn})
		}
	}
	rankedTags = append(special, rankedTags...)

	numTags := len(rankedTags)
	lowerBound := 0
	upperBound := numTags
	bestTree := ""
	bestTreeTokens := 0.0

	chatRelFnames := map[string]bool{}
	for _, f := range chatFnames {
		chatRelFnames[rm.relFname(f)] = true
	}

	middle := min(maxMapTokens/25, numTags)
	for lowerBound <= upperBound {
		tree := rm.toTree(inv, rankedTags[:middle], chatRelFnames)
		numTokens := tokenEstimate(tree)

		pctErr := abs(numTokens-float64(maxMapTokens)) / float64(maxMapTokens)
		const okErr = 0.15
		if (numTokens <= float64(maxMapTokens) && numTokens > bestTreeTokens) || pctErr < okErr {
			bestTree = tree
			bestTreeTokens = numTokens
			if pctErr < okErr {
				break
			}
		}

		if numTokens < float64(maxMapTokens) {
			lowerBound = middle + 1
		} else {
			upperBound = middle - 1
		}
		middle = (lowerBound + upperBound) / 2
	}

	return bestTree
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// toTree ports to_tree: group sorted items by file, render tag files
// through TreeContext, emit bare files as a name line, skip chat files,
// truncate lines to 100 runes.
func (rm *RepoMap) toTree(inv *invocation, items []MapItem, chatRelFnames map[string]bool) string {
	if len(items) == 0 {
		return ""
	}

	sorted := append([]MapItem(nil), items...)
	slices.SortFunc(sorted, func(a, b MapItem) int {
		if lessMapItem(a, b) {
			return -1
		}
		if lessMapItem(b, a) {
			return 1
		}
		return 0
	})

	curFname := "\x00sentinel-unset"
	curAbsFname := ""
	var lois []int
	haveLois := false
	var output strings.Builder

	flush := func() {
		if haveLois {
			output.WriteString("\n")
			output.WriteString(curFname + ":\n")
			output.WriteString(rm.renderTree(inv, curAbsFname, curFname, lois))
			lois, haveLois = nil, false
		} else if curFname != "\x00sentinel-unset" && curFname != "" {
			output.WriteString("\n" + curFname + "\n")
		}
	}

	for _, item := range append(sorted, MapItem{RelFname: ""}) { // sentinel flush
		if chatRelFnames[item.RelFname] {
			continue
		}
		if item.RelFname != curFname {
			flush()
			if item.Tag != nil {
				lois = []int{}
				haveLois = true
				curAbsFname = item.Tag.Fname
			}
			curFname = item.RelFname
		}
		if haveLois && item.Tag != nil {
			lois = append(lois, item.Tag.Line)
		}
	}

	// Truncate long lines (minified files), by runes; ensure trailing
	// newline.
	rawLines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	var b strings.Builder
	for _, line := range rawLines {
		if utf8.RuneCountInString(line) > 100 {
			runes := []rune(line)
			line = string(runes[:100])
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderTree ports render_tree: normalize the source, build (or
// reuse, per invocation) the TreeContext, select lines of interest, format.
func (rm *RepoMap) renderTree(inv *invocation, absFname, relFname string, lois []int) string {
	sortedLois := append([]int(nil), lois...)
	slices.Sort(sortedLois)
	key := relFname + "\x00" + fmt.Sprint(sortedLois)
	if res, ok := inv.rendered[key]; ok {
		return res
	}

	ctx, ok := inv.treeCtx[relFname]
	if !ok {
		ctx = rm.buildTreeContext(absFname)
		inv.treeCtx[relFname] = ctx
	}
	if ctx == nil {
		inv.rendered[key] = ""
		return ""
	}

	ctx.SetLinesOfInterest(sortedLois)
	ctx.AddContext()
	res := ctx.Format()
	inv.rendered[key] = res
	return res
}

func (rm *RepoMap) buildTreeContext(absFname string) *TreeContext {
	src, err := os.ReadFile(absFname)
	if err != nil {
		return nil
	}
	code := string(src)
	if !strings.HasSuffix(code, "\n") {
		code += "\n"
	}
	// Reuse the invocation's parse when the source is identical; the tags
	// parse used the raw file, which only differs by the appended \n.
	lang := filenameToLang(absFname)
	if lang == "" {
		return nil
	}
	entry, err := langFor(lang)
	if err != nil || entry == nil {
		return nil
	}
	parser := ts.NewParser(entry.language)
	tree, err := parser.Parse([]byte(code))
	if err != nil || tree == nil {
		return nil
	}
	return NewTreeContext(code, tree.RootNode())
}

// Tags extracts the definition and reference tags for the given absolute paths.
// It is the entry point behind the symbol tool: the same tree-sitter pass the
// map is built from, exposed without the ranking and rendering on top.
//
// Files with no grammar, no tags query, or unreadable contents contribute
// nothing and are skipped silently — a missing grammar is a gap in coverage,
// not an error in the caller's request.
func (rm *RepoMap) Tags(absFnames []string) []Tag {
	if rm.warnedFiles == nil {
		rm.warnedFiles = map[string]bool{}
	}
	inv := newInvocation()
	out := make([]Tag, 0, len(absFnames))
	for _, fname := range absFnames {
		out = append(out, inv.tags(rm, fname, rm.relFname(fname))...)
	}
	return out
}

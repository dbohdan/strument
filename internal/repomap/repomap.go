package repomap

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
)

// RepoMap generates the ranked repository map. There is no cross-call cache:
// every GetRepoMap call re-tags and re-ranks; compiled
// queries are process-lifetime; parses and TreeContexts are memoized only
// within one call.
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

// tags returns the memoized tag extraction for fname.
func (inv *invocation) tags(rm *RepoMap, fname, relFname string) []Tag {
	if t, ok := inv.fileTags[relFname]; ok {
		return t
	}
	var t []Tag
	if rm.TagsOverride != nil {
		t = rm.TagsOverride(fname, relFname)
	} else {
		t = extractTags(relFname, fname, inv.parse(rm, fname, relFname))
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

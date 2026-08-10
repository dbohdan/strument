package repomap

import (
	"fmt"
	"os"
	"path/filepath"
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
	Root string
	Warn func(format string, args ...any)

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

// New builds a RepoMap over root. What it is now is a tag layer: Tags for
// symbol and /symbol, ParseStatus for the after-an-edit check. The ranked map
// it is named for is gone — see the commit that removed it.
func New(root string) *RepoMap {
	return &RepoMap{
		Root:        root,
		warnedFiles: map[string]bool{},
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

// invocation carries the per-call memoization: parse trees and tags, discarded
// when the call returns. The tags outlive it in RepoMap's cache; the trees and
// sources are the big objects and do not.
type invocation struct {
	parsed   map[string]*parsedFile
	fileTags map[string][]Tag
}

func newInvocation() *invocation {
	return &invocation{
		parsed:   map[string]*parsedFile{},
		fileTags: map[string][]Tag{},
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
				rm.warnf("Skipping non-UTF-8 file %s", fname)
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

// Tags extracts the definition and reference tags for the given absolute paths.
// It is the entry point behind the symbol tool and the /symbol command, and
// since the ranked map was removed it is what this package is for.
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

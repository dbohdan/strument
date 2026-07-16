package repomap

import (
	"embed"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

//go:embed queries/*-tags.scm
var queriesFS embed.FS

// extToLang is grep_ast's PARSERS map filtered to the 31 languages that ship
// a tags query in aider's tree-sitter-language-pack directory (repomap-spec
// §1.2). Lookup is by exact extension, case-sensitive, like grep_ast's
// filename_to_lang.
var extToLang = map[string]string{
	".ino":  "arduino",
	".bash": "bash", ".sh": "bash", ".zsh": "bash",
	".c": "c", ".h": "c",
	".chatito": "chatito",
	".clj":     "clojure", ".cljc": "clojure", ".cljs": "clojure", ".edn": "clojure",
	".cl": "commonlisp", ".lisp": "commonlisp",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".h++": "cpp", ".hpp": "cpp", ".hxx": "cpp",
	".cs":   "csharp",
	".d":    "d",
	".dart": "dart",
	".el":   "elisp",
	".ex":   "elixir", ".exs": "elixir",
	".elm":   "elm",
	".gleam": "gleam",
	".go":    "go",
	".java":  "java",
	".js":    "javascript", ".jsx": "javascript", ".mjs": "javascript",
	".lua": "lua",
	".m":   "matlab", ".mat": "matlab",
	".ml":         "ocaml",
	".mli":        "ocaml_interface",
	".pony":       "pony",
	".properties": "properties",
	".py":         "python",
	".R":          "r", ".r": "r",
	".rkt":   "racket",
	".rb":    "ruby",
	".rs":    "rust",
	".sol":   "solidity",
	".swift": "swift",
	".rules": "udev",
}

// grammarName maps aider/grep_ast language names to gotreesitter registry
// names where they differ.
var grammarName = map[string]string{
	"csharp": "c_sharp",
}

// filenameToLang ports grep_ast's filename_to_lang for the supported set.
func filenameToLang(fname string) string {
	return extToLang[path.Ext(fname)]
}

// setAdjacentRe strips the #set-adjacent! directive, which py-tree-sitter
// (aider's engine) ignores as an unknown tags-crate directive but
// gotreesitter rejects at compile time. All occurrences target only @doc
// captures, which the mapper never reads, so removal is behavior-preserving.
var setAdjacentRe = regexp.MustCompile(`\(#set-adjacent![^)]*\)`)

// preprocessQuery adapts aider's tags queries to gotreesitter's query
// engine. Two adjustments, both behavior-preserving for the name.* captures
// the mapper consumes (see STATUS.md, phase 3):
//
//  1. Remove `(#set-adjacent! ...)` — unsupported directive (see above).
//  2. Remove the `(comment)* @doc` + `.` anchor prefix and the @doc
//     directives from doc-comment patterns: gotreesitter matches that
//     anchored-sibling shape only once per parent (upstream tree-sitter
//     matches at every position), which silently drops most definitions.
//     Upstream, `(comment)*` matches zero comments, so the prefix never
//     constrains which definitions match — only what @doc binds to, which
//     the mapper ignores.
func preprocessQuery(src string) string {
	src = setAdjacentRe.ReplaceAllString(src, "")
	lines := strings.Split(src, "\n")
	var out []string
	skipAnchor := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "(comment)* @doc" || trimmed == "(comment)+ @doc" {
			skipAnchor = true
			continue
		}
		if skipAnchor && trimmed == "." {
			skipAnchor = false
			continue
		}
		skipAnchor = false
		if strings.Contains(trimmed, "! @doc") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// langEntry is the process-lifetime compiled state for one language
// (repomap-spec §0.3: compiled queries live for the process).
type langEntry struct {
	language *ts.Language
	query    *ts.Query
	err      error
}

var (
	langMu    sync.Mutex
	langCache = map[string]*langEntry{}
)

// langFor returns the grammar and compiled tags query for a language name,
// or nil when the language has no grammar in gotreesitter (the file then
// becomes a bare entry, repomap-spec §3.6). A query compile failure is an
// error: the query is embedded, so failure means a build problem (§7).
func langFor(lang string) (*langEntry, error) {
	langMu.Lock()
	defer langMu.Unlock()
	if e, ok := langCache[lang]; ok {
		return e, e.err
	}

	gname := lang
	if n, ok := grammarName[lang]; ok {
		gname = n
	}
	reg := grammars.DetectLanguageByName(gname)
	if reg == nil {
		langCache[lang] = nil
		return nil, nil //nolint:nilnil // No grammar => bare entry (§3.6), not an error.
	}

	src, err := queriesFS.ReadFile("queries/" + lang + "-tags.scm")
	if err != nil {
		langCache[lang] = nil
		return nil, nil //nolint:nilnil // No query => bare entry (§3.6), not an error.
	}
	qsrc := preprocessQuery(string(src))

	language := reg.Language()
	q, err := ts.NewQuery(qsrc, language)
	entry := &langEntry{language: language, query: q}
	if err != nil {
		entry.err = fmt.Errorf("compile %s-tags.scm: %w", lang, err)
	}
	langCache[lang] = entry
	return entry, entry.err
}

// SupportedLanguages returns the language names with both an embedded query
// and a gotreesitter grammar; used by the startup self-test.
func SupportedLanguages() []string {
	entries, _ := queriesFS.ReadDir("queries")
	var langs []string
	for _, e := range entries {
		name := e.Name()
		lang := name[:len(name)-len("-tags.scm")]
		gname := lang
		if n, ok := grammarName[lang]; ok {
			gname = n
		}
		if grammars.DetectLanguageByName(gname) != nil {
			langs = append(langs, lang)
		}
	}
	return langs
}

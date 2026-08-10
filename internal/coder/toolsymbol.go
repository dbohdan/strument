package coder

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
)

// maxSymbolSites caps how many places one answer names. A name defined once and
// used five hundred times should say so and stop, not spend the context window
// proving it.
const maxSymbolSites = 100

// symbolTool answers "where is this defined" from the language parser rather
// than from text.
//
// It is offered beside grep, not instead of it, and its description has to keep
// them apart: two tools that look like they do the same thing produce selection
// errors, and the model picks by description. So grep finds text anywhere —
// comments, strings, a name inside a longer name — and symbol finds the places
// a name is declared.
//
// Whether a model reaches for it at all is an open question. They reach hard for
// grep, which is part of what started this pivot. Built to be watched.
func symbolTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolSymbol,
		Description: "Find where a name is defined, using the language parser. Give an exact " +
			"identifier, not a pattern: this matches declarations, not text, so it will not " +
			"report the name in a comment, in a string, or as part of a longer name. " +
			"Use grep instead when you want text anywhere in the project.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": strProp("The exact identifier, e.g. \"runAutoVerify\" or \"Workspace\"."),
				"kind": map[string]any{
					"type": "string",
					"enum": []any{"definition", "reference"},
					"description": "\"definition\" (the default) reports where the name is declared; " +
						"\"reference\" reports where it is used, naming the function each use sits in " +
						"where the parser can tell.",
				},
			},
			"required": []any{"name"},
		},
	}
}

// runSymbol answers a symbol call.
func (c *Coder) runSymbol(tc llm.ToolCall) string {
	var a struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	text, count, problem := c.SymbolLookup(a.Name, a.Kind)
	if problem != "" {
		return problem
	}
	c.Out.Toolf("Looked up %s — %s %s", strings.TrimSpace(a.Name),
		plural(count, "site", "sites"), lookupNoun(a.Kind))
	return truncateResult(text)
}

// lookupNoun is the word for what was looked for, shared by the outcome line
// and the answer so the two agree.
func lookupNoun(kind string) string {
	if kind == "reference" {
		return "referenced"
	}
	return "defined"
}

// SymbolLookup answers "where is this name defined, or used" for a model and a
// human alike — /symbol shows the same text the tool returns, because the
// answer to that question does not depend on who asked.
//
// count is how many sites it names, which the tool path reports on its own
// outcome line; the REPL has the text and needs no count. problem is a
// caller-facing failure message, "" on success — the same shape decodeArgs and
// parseCommandArgs use, because these read as sentences to a model and a person
// alike rather than as Go error strings.
func (c *Coder) SymbolLookup(rawName, kind string) (text string, count int, problem string) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "", 0, "The required \"name\" argument was missing."
	}
	if c.RepoMap == nil {
		// Unreachable through toolDefs, which offers the tool only with the
		// parse layer behind it, and guarded in the REPL the same way. Answered
		// rather than panicked, because a tool call arrives from outside and
		// outside is where surprises come from.
		return "", 0, "The language parser is not available in this session; use grep to find the text."
	}
	want := repomap.Def
	switch kind {
	case "", "definition":
	case "reference":
		want = repomap.Ref
	default:
		return "", 0, fmt.Sprintf("Unknown kind %q. Use \"definition\" or \"reference\".", kind)
	}

	rels, trunc, ferr := c.Files.Files()
	if ferr != nil {
		return "", 0, fmt.Sprintf("Could not list the project's files: %v", ferr)
	}
	abs := make([]string, 0, len(rels))
	for _, rel := range rels {
		abs = append(abs, filepath.Join(c.Root, filepath.FromSlash(rel)))
	}

	var sites []string
	for _, t := range c.RepoMap.Tags(abs) {
		if t.Name != name || t.Kind != want {
			continue
		}
		if t.Line < 0 {
			// A backfilled reference with no position: it says the file uses
			// the name but not where, and a line number of 0 would be a lie.
			sites = append(sites, t.RelFname)
			continue
		}
		site := fmt.Sprintf("%s:%d", t.RelFname, t.Line+1)
		// Naming the function a reference sits in is what turns a list of
		// coordinates into an answer about the code. Only an extractor that
		// knows exactly fills Enclosing in, so a site with no name attached is
		// one nothing could be said about — never a guess. Silence is the
		// honest form here: a wrong function name sends a reader somewhere real
		// and wrong, and this tool earns its place beside grep by being exact.
		if t.Enclosing != "" {
			site += "  in " + t.Enclosing
		}
		sites = append(sites, site)
	}
	// One tag can be extracted twice when a query matches overlapping patterns,
	// and the same site listed twice reads as two separate places. The
	// enclosing name is part of the key, or an annotated and an unannotated
	// copy of one site would both survive.
	slices.Sort(sites)
	sites = slices.Compact(sites)

	noun := lookupNoun(kind)
	if len(sites) == 0 {
		return fmt.Sprintf("No place where %s is %s was found. The parser only sees languages it has a "+
			"grammar for; grep will find the text wherever it is.", name, noun), 0, ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s is %s in %s:\n\n", name, noun, plural(len(sites), "place", "places"))
	for i, s := range sites {
		if i >= maxSymbolSites {
			fmt.Fprintf(&b, "\n(%d more, not listed.)\n", len(sites)-maxSymbolSites)
			break
		}
		fmt.Fprintf(&b, "%s\n", s)
	}
	if trunc.Any() {
		b.WriteString("\n(The file walk hit a limit, so some of the project was not searched.)\n")
	}
	return b.String(), len(sites), ""
}

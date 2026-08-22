package coder

import (
	"fmt"
	"os"
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
		Description: "Answer a question about an identifier from the language parser. " +
			"\"Where is this declared?\" is kind \"definition\"; \"what calls this?\" or " +
			"\"where is this used?\" is kind \"reference\". A reference names the function " +
			"each use sits in, which grep cannot tell you: a search hit plus a few lines of " +
			"context does not say which function you are inside, and guessing it from " +
			"nearby code is where wrong answers come from. Each site comes back with its " +
			"source line, so there is nothing to read afterwards. Give an exact identifier, " +
			"not a pattern: this matches declarations, not text, so it will not report the " +
			"name in a comment, in a string, or as part of a longer name. Use grep when you " +
			"want text anywhere in the project, or when you do not know the exact name.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": strProp("The exact identifier, e.g. \"runAutoCheck\" or \"Workspace\"."),
				"kind": map[string]any{
					"type": "string",
					"enum": []any{"definition", "reference"},
					"description": "\"definition\" (the default) reports the one or few places the " +
						"name is declared. \"reference\" reports every place it is used, each with " +
						"its file, line, source, and the function it sits in — that is the one to " +
						"ask for when the question is who calls something.",
				},
			},
			"required": []any{"name"},
		},
	}
}

// runSymbol answers a symbol call.
func (i *Inspector) runSymbol(tc llm.ToolCall) string {
	var a struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	text, count, problem := i.SymbolLookup(a.Name, a.Kind)
	if problem != "" {
		return problem
	}
	i.Out.Toolf("Looked up %s — %s %s", quoteToolArg(strings.TrimSpace(a.Name)),
		plural(count, "site", "sites"), lookupNoun(a.Kind))
	return truncateResult(text)
}

// maxSymbolLine caps one echoed source line. A minified or generated file can
// hold a single line thousands of characters long, and one of those would cost
// more than the whole rest of the answer.
const maxSymbolLine = 200

// sourceLines reads a file once and hands back single lines from it, so an
// answer naming forty sites in three files opens three files.
type sourceLines struct {
	root  string
	byRel map[string][]string
}

func newSourceLines(root string) *sourceLines {
	return &sourceLines{root: root, byRel: map[string][]string{}}
}

// at returns the trimmed source at a 0-based line, or "" when it cannot be had.
// Every failure is silent: the site itself is still a true answer, and a
// tool that refused to report a location because it could not echo the line
// would be worse than one that reports the location alone.
func (s *sourceLines) at(rel string, line int) string {
	if line < 0 {
		return ""
	}
	lines, ok := s.byRel[rel]
	if !ok {
		data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
		if err != nil {
			s.byRel[rel] = nil
			return ""
		}
		lines = strings.Split(string(data), "\n")
		s.byRel[rel] = lines
	}
	if line >= len(lines) {
		return ""
	}
	text := strings.TrimSpace(strings.ReplaceAll(lines[line], "\t", " "))
	if len(text) > maxSymbolLine {
		text = text[:maxSymbolLine] + "…"
	}
	return text
}

// missMessage explains a lookup that found nothing, without inventing a cause.
//
// It used to say "the parser only sees languages it has a grammar for", which
// in a Go project is the wrong reason and usually a false one: the grammar was
// there, the name simply was not a tagged declaration. A miss that hands back a
// wrong explanation is worse than one that hands back none — it generalizes,
// and a model that reads "the parser might not know this language" once has
// been told to stop trusting the tool for the rest of the session.
//
// So: report what was actually checked, and use what the same pass already
// learned. A name found under the other kind, or under a different case, is the
// answer the caller wanted and is one call away.
func missMessage(name, kind, noun string, otherKind int, nearMiss string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "No place where %s is %s was found.\n", name, noun)

	if otherKind > 0 {
		other := "reference"
		if kind == "reference" {
			other = "definition"
		}
		fmt.Fprintf(&b, "It does appear as a %s in %s — call symbol again with \"kind\": %q.\n",
			other, plural(otherKind, "place", "places"), other)
		return b.String()
	}
	if nearMiss != "" {
		fmt.Fprintf(&b, "The project does declare %s, which differs only in case.\n", nearMiss)
		return b.String()
	}

	b.WriteString("symbol reports declarations the parser found: functions, methods, types, " +
		"struct fields, constants, and variables. A name that is only a parameter, a word in a " +
		"comment or a string, part of a longer name, or in a file with no grammar will not be " +
		"here. grep finds text wherever it is.\n")
	return b.String()
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
func (i *Inspector) SymbolLookup(rawName, kind string) (text string, count int, problem string) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "", 0, "The required \"name\" argument was missing."
	}
	if i.RepoMap == nil {
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

	rels, trunc, ferr := i.Files.Files()
	if ferr != nil {
		return "", 0, fmt.Sprintf("Could not list the project's files: %v", ferr)
	}
	abs := make([]string, 0, len(rels))
	for _, rel := range rels {
		abs = append(abs, filepath.Join(i.Root, filepath.FromSlash(rel)))
	}

	// Everything about this name, in one pass: the sites asked for, plus what
	// is needed to explain a miss without guessing at the cause.
	var sites []string
	src := newSourceLines(i.Root)
	otherKind := 0
	var nearMiss string
	lowered := strings.ToLower(name)
	for _, t := range i.RepoMap.Tags(abs) {
		if t.Name != name {
			if nearMiss == "" && strings.ToLower(t.Name) == lowered {
				nearMiss = t.Name
			}
			continue
		}
		if t.Kind != want {
			otherKind++
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
		// The source line, so the answer is an answer rather than a coordinate
		// to go and look up. Without it every site costs a follow-up read, and
		// grep — which returns the matching line in one call — wins the
		// comparison on call count even where symbol is the better instrument.
		if line := src.at(t.RelFname, t.Line); line != "" {
			site += "\n    " + line
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
		return missMessage(name, kind, noun, otherKind, nearMiss), 0, ""
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
	// A definition answer says where the name lives, which is not what "who
	// calls this" asked. kind defaults to definition, so a model asking the
	// natural first question of a callers question gets an answer to a
	// different one — GLM-5.3 did exactly that in
	// doc/experiments/2026-08-symbol-uptake.md, and half the time took the
	// one-site answer as the tool's last word and went back to grep. The miss
	// path already offers the other kind; the *hit* path is where it was
	// needed, because a confident short answer is the one nobody follows up.
	//
	// Only on the definition side. A reference answer is already what a
	// callers question wanted, and pointing back at the declaration there
	// would be a line of noise on the common case.
	if want == repomap.Def && otherKind > 0 {
		fmt.Fprintf(&b, "\nIt is used in %s. To see them, with the function each use sits in, "+
			"call symbol again with \"kind\": \"reference\".\n",
			plural(otherKind, "place", "places"))
	}
	if trunc.Any() {
		b.WriteString("\n(The file walk hit a limit, so some of the project was not searched.)\n")
	}
	return b.String(), len(sites), ""
}

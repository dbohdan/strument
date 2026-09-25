package coder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dbohdan.com/strument/internal/repomap"
)

// Outlines in the read tool.
//
// A file longer than the read window used to come back as its first 2,000
// lines and a note to read on from an offset, which is a page number with no
// table of contents. maki's `index` tool (4cefcff) gives a file's skeleton
// with line ranges so a model reads the part it needs; its FrontierHarness
// run shows the model using it on repository tasks and reading about 100
// lines at a time. Here the same tree-sitter layer the webfetch outline and
// `symbol` use draws the map, in two places:
//
//   - a read that stops short of the end says what the rest of the file
//     defines, and where, without being asked;
//   - `outline: true` returns the map alone.
//
// readArm selects which of these a build offers, for the trial in
// doc/experiments/2026-09-read-outline. It is set at link time
// (-ldflags "-X dbohdan.com/strument/internal/coder.readArm=C"):
//
//	"" or "A"  neither (the baseline)
//	"B"        the outline of the rest on a truncated read
//	"C"        B, plus the outline parameter
//	"D"        C, plus limit required, as maki requires it
var readArm = ""

func readOutlineOnTruncation() bool { return readArm >= "B" }
func readOutlineParam() bool        { return readArm >= "C" }
func readLimitRequired() bool       { return readArm >= "D" }

// maxOutlineEntries caps an outline. A file with more definitions than this
// is one to search rather than survey, and the note says how many were left.
const maxOutlineEntries = 200

// fileOutline parses a project file and returns its definitions, or known
// false when there is no grammar for it or it does not parse.
func (i *Inspector) fileOutline(rel string) (defs []repomap.DefOutline, known bool) {
	src, err := os.ReadFile(filepath.Join(i.Root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	return repomap.DefOutlines(rel, src)
}

// formatOutline renders definitions one per line, indented by nesting, each
// with the lines it spans: the coordinates read's offset and limit take.
// keep filters which are listed; nil lists all.
func formatOutline(defs []repomap.DefOutline, keep func(repomap.DefOutline) bool) (string, int) {
	var b strings.Builder
	listed, omitted := 0, 0
	for _, d := range defs {
		if keep != nil && !keep(d) {
			continue
		}
		if listed == maxOutlineEntries {
			omitted++
			continue
		}
		sig := d.Signature
		if sig == "" {
			sig = d.Kind + " " + d.Name
		}
		fmt.Fprintf(&b, "%s- %s  [%d-%d]\n", strings.Repeat("  ", d.Depth), sig, d.Start, d.End)
		listed++
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d more definitions not listed; grep for the one you want.)\n", omitted)
	}
	return b.String(), listed + omitted
}

// restOutline is what a truncated read adds: the definitions that lie wholly
// or partly outside the lines shown. "" when there is nothing to add.
func (i *Inspector) restOutline(rel string, first, last int) string {
	defs, known := i.fileOutline(rel)
	if !known || len(defs) == 0 {
		return ""
	}
	text, n := formatOutline(defs, func(d repomap.DefOutline) bool {
		return d.Start < first || d.End > last
	})
	if n == 0 {
		return ""
	}
	return "\nOutside these lines, the file defines:\n" + text +
		"Read one by its line range, as offset and limit.\n"
}

// outlineResult answers read with outline: true.
func (i *Inspector) outlineResult(rel string) string {
	defs, known := i.fileOutline(rel)
	if !known {
		return fmt.Sprintf("There is no outline for %s: no language parser covers it, or it does not "+
			"parse. Read it with offset and limit instead.", quoteToolArg(rel))
	}
	if len(defs) == 0 {
		return quoteToolArg(rel) + " parses but defines nothing at file scope. Read it with offset and limit."
	}
	text, _ := formatOutline(defs, nil)
	i.Out.Toolf("Outlined %s (%s)", quoteToolArg(rel), plural(len(defs), "definition", "definitions"))
	return fmt.Sprintf("%s, outline (each definition's lines in brackets):\n%s"+
		"Read the part you need with offset and limit.", rel, text)
}

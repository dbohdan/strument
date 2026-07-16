// Transliterated from aider tests/basic/test_find_or_blocks.py @ 5dc9490:
// run the 4 MB chat-history corpus through the parser and compare
// byte-for-byte against the gold output.
package editblock

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

var sectionRe = regexp.MustCompile(`####\s`)

// processMarkdown ports process_markdown from test_find_or_blocks.py.
func processMarkdown(content string, out *strings.Builder) {
	// Python: re.split(r"(?=####\s)", content) — split before each match.
	var sections []string
	locs := sectionRe.FindAllStringIndex(content, -1)
	prev := 0
	for _, loc := range locs {
		if loc[0] > prev {
			sections = append(sections, content[prev:loc[0]])
		}
		prev = loc[0]
	}
	sections = append(sections, content[prev:])

	ats := strings.Repeat("@", 20)

	for _, section := range sections {
		if strings.Contains(section, "editblock_coder.py") || strings.Contains(section, "test_editblock.py") {
			continue
		}
		if strings.TrimSpace(section) == "" {
			continue
		}
		header := strings.TrimSpace(strings.SplitN(section, "\n", 2)[0])
		lines := splitLines(section)
		var body string
		if len(lines) > 1 {
			body = strings.Join(lines[1:], "")
		}

		// Pick the first fence (rotated list, entry 0 last) whose opener
		// appears in the body after a newline.
		fence := AllFences[0]
		rotated := append(append([]Fence(nil), AllFences[1:]...), AllFences[0])
		for _, f := range rotated {
			fence = f
			if strings.Contains(body, "\n"+f.Open) {
				break
			}
		}

		blocks, err := FindBlocks(body, fence, nil)
		if err != nil {
			fmt.Fprintf(out, "\n\n@@@ %s %s\n", header, ats)
			fmt.Fprintf(out, "%s\n", err.Error())
			continue
		}

		if len(blocks) > 0 {
			fmt.Fprintf(out, "\n\n@@@ %s %s\n", header, ats)
		}

		for _, block := range blocks {
			if block.IsShell {
				fmt.Fprintf(out, "@@@ SHELL %s\n", ats)
				out.WriteString(block.Shell)
				fmt.Fprintf(out, "@@@ ENDSHELL %s\n", ats)
			} else {
				fmt.Fprintf(out, "@@@ SEARCH: %s %s\n", block.Edit.Path, ats)
				out.WriteString(block.Edit.Search)
				fmt.Fprintf(out, "%s\n", ats)
				out.WriteString(block.Edit.Replace)
				fmt.Fprintf(out, "@@@ REPLACE %s\n", ats)
			}
		}
	}
}

func TestProcessMarkdownGolden(t *testing.T) {
	input, err := os.ReadFile("../../testdata/transliterated/editblock/chat-history.md")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../testdata/transliterated/editblock/chat-history-search-replace-gold.txt")
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	processMarkdown(string(input), &out)
	got := out.String()

	if got != string(want) {
		gotLines := strings.SplitAfter(got, "\n")
		wantLines := strings.SplitAfter(string(want), "\n")
		n := min(len(gotLines), len(wantLines))
		for i := 0; i < n; i++ {
			if gotLines[i] != wantLines[i] {
				lo := max(0, i-3)
				hi := min(n, i+4)
				var ctx strings.Builder
				for k := lo; k < hi; k++ {
					fmt.Fprintf(&ctx, "%6d want: %q\n%6d  got: %q\n", k+1, wantLines[k], k+1, gotLines[k])
				}
				t.Fatalf("first difference at line %d:\n%s", i+1, ctx.String())
			}
		}
		t.Fatalf("outputs differ in length: got %d lines, want %d lines", len(gotLines), len(wantLines))
	}
}

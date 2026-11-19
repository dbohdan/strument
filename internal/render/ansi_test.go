package render

import (
	"regexp"
	"strings"
	"testing"
)

const ansiSampleDoc = "# Title\n" +
	"\n" +
	"Some *italic* and **bold** and `code`.\n" +
	"\n" +
	"> quoted line\n" +
	"\n" +
	"- item one\n" +
	"- item two\n" +
	"   - nested\n" +
	"\n" +
	"3. third\n" +
	"4. fourth\n" +
	"\n" +
	"```go\nfunc main() {}\n```\n" +
	"\n" +
	"[label](https://x.example) and https://z.example plain.\n" +
	"\n" +
	"---\n"

// testRuleWidth keeps the horizontal rule at 40 columns so the layout
// assertions below stay stable regardless of the real terminal.
const testRuleWidth = 40

func renderANSI(doc string, color bool, byChar bool) string {
	var sb strings.Builder
	p := NewParser(NewANSI(&sb, color, DefaultTheme(), testRuleWidth))
	if byChar {
		for _, c := range doc {
			p.Write(string(c))
		}
	} else {
		p.Write(doc)
	}
	p.End()
	return sb.String()
}

func TestANSIPlainLayout(t *testing.T) {
	got := renderANSI(ansiSampleDoc, false, false)

	for _, want := range []string{
		"# Title",
		"Some italic and bold and code.",
		"│ quoted line",
		"• item one",
		"• item two",
		"  • nested",
		"3. third",
		"4. fourth",
		"func main() {}",
		"label (https://x.example) and https://z.example plain.",
		strings.Repeat("-", 40),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "\x1b") {
		t.Error("plain mode must not emit escape codes")
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("more than one blank line between blocks:\n%q", got)
	}
}

func TestANSIChunkInvariance(t *testing.T) {
	whole := renderANSI(ansiSampleDoc, true, false)
	byChar := renderANSI(ansiSampleDoc, true, true)
	if whole != byChar {
		t.Errorf("streaming granularity changed the output:\nwhole:\n%q\nby char:\n%q", whole, byChar)
	}
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestANSIColorMatchesPlain(t *testing.T) {
	color := renderANSI(ansiSampleDoc, true, false)
	plain := renderANSI(ansiSampleDoc, false, false)

	if got := sgrRe.ReplaceAllString(color, ""); got != plain {
		t.Errorf("stripped color output differs from plain:\n%q\nvs\n%q", got, plain)
	}
	if !strings.Contains(color, "\x1b[1m") || !strings.Contains(color, "\x1b[37m") {
		t.Error("expected bold and white styles in color output")
	}
}

func TestANSITable(t *testing.T) {
	doc := "| Name | Age |\n| --- | --- |\n| Ada | 36 |\n"
	got := renderANSI(doc, false, false)
	for _, want := range []string{" Name  │  Age ", " Ada  │  36 "} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q:\n%q", want, got)
		}
	}
	if !strings.Contains(got, "─") {
		t.Errorf("missing header underline:\n%q", got)
	}
}

func TestANSIAssistantBaseColor(t *testing.T) {
	var sb strings.Builder
	p := NewParser(NewANSI(&sb, true, DefaultTheme(), testRuleWidth))
	p.Write("text `code` more\n")
	p.End()
	got := sb.String()

	base := "\x1b[38;2;0;136;255m" // DefaultTheme assistant, #0088ff
	if !strings.HasPrefix(got, base) {
		t.Errorf("assistant base color not emitted before the first byte:\n%q", got)
	}
	// The base is re-applied after the code span's style is popped, so it
	// appears at least twice (initial + reapply).
	if strings.Count(got, base) < 2 {
		t.Errorf("base color not restored after a nested style pop:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[37m") {
		t.Errorf("distinct code color missing:\n%q", got)
	}
}

func TestANSIRuleWidth(t *testing.T) {
	for _, tc := range []struct{ width, want int }{{20, 20}, {0, 80}} {
		var sb strings.Builder
		p := NewParser(NewANSI(&sb, false, Theme{}, tc.width))
		p.Write("---\n")
		p.End()
		if !strings.Contains(sb.String(), strings.Repeat("-", tc.want)) {
			t.Errorf("width %d: rule not %d columns:\n%q", tc.width, tc.want, sb.String())
		}
	}
}

func TestANSICheckbox(t *testing.T) {
	got := renderANSI("- [x] done\n- [ ] todo\n", false, false)
	if !strings.Contains(got, "[x] done") || !strings.Contains(got, "[ ] todo") {
		t.Errorf("checkbox output wrong:\n%q", got)
	}
}

// TestANSIUnderscoreIsNeverEmphasis pins that "_" renders as itself wherever it
// is. In a model's answer an underscore at a word's edge belongs to an
// identifier — __init__, _private, snake_case — and as emphasis it mangled the
// name: __init__ came out as a bold "init". Models emphasise with "*". byChar
// exercises it across chunk boundaries.
func TestANSIUnderscoreIsNeverEmphasis(t *testing.T) {
	for _, doc := range []string{
		"ansi_text.go\n",
		"a_b_c\n",
		"snake_case_name\n",
		"path/to/my_file_v2.py\n",
		"var_1 x_2\n",
		"__init__\n",
		"call __init__ and _private_helper_ here\n",
		"_em_\n",
	} {
		want := strings.TrimRight(doc, "\n")
		for _, byChar := range []bool{false, true} {
			got := renderANSI(doc, true, byChar)
			if !strings.Contains(got, want) || strings.Contains(got, "\x1b[3m") || strings.Contains(got, "\x1b[1m") {
				t.Errorf("byChar=%v: %q rendered %q, want the underscores literal and no emphasis", byChar, doc, got)
			}
		}
	}
}

// TestANSIAsteriskEmphasisStillWorks: "*" is how models emphasise, including
// intraword, which CommonMark allows. color is on so the italic ("3") / bold
// ("1") SGR is observable.
func TestANSIAsteriskEmphasisStillWorks(t *testing.T) {
	cases := map[string]string{
		"a *b* c\n":   "\x1b[3m",
		"x*y*z\n":     "\x1b[3m",
		"a **b** c\n": "\x1b[1m",
		"_x_ **b**\n": "\x1b[1m",
	}
	for doc, want := range cases {
		if got := renderANSI(doc, true, false); !strings.Contains(got, want) {
			t.Errorf("%q rendered %q, want emphasis SGR %q", doc, got, want)
		}
	}
}

// TestANSIBareBracketsAreText: a "[" opens a link only once "](" follows the
// label. "[WIP]", "[1]" and arr[i] are what models write; none is a link, and
// the brackets stay.
func TestANSIBareBracketsAreText(t *testing.T) {
	for _, doc := range []string{
		"[WIP] parser\n",
		"see [1] and [2]\n",
		"arr[i] = arr[j]\n",
		"a [b] (c)\n",
		"unclosed [label\n",
		"unclosed at end [lab",
		"[" + strings.Repeat("x", maxLinkLabel+10) + "](u)\n",
		"[split\nlines](u)\n",
	} {
		want := strings.TrimRight(doc, "\n")
		for _, byChar := range []bool{false, true} {
			got := renderANSI(doc, false, byChar)
			for line := range strings.SplitSeq(want, "\n") {
				if !strings.Contains(got, line) {
					t.Errorf("byChar=%v: %q rendered %q, missing %q", byChar, doc, got, line)
				}
			}
		}
	}
}

// TestANSILinksStillRender: "[t](u)" is still a link, drawn as the label and
// the target after it, and markup inside the label still applies.
func TestANSILinksStillRender(t *testing.T) {
	cases := map[string]string{
		"[title](https://example.com)\n":       "title (https://example.com)",
		"see [the docs](https://x.y/z) here\n": "see the docs (https://x.y/z) here",
		"[a] then [b](u)\n":                    "[a] then b (u)",
		"[**bold** label](u)\n":                "bold label (u)",
		"- [x] done\n- [ ] todo [ref](u)\n":    "todo ref (u)",
	}
	for doc, want := range cases {
		for _, byChar := range []bool{false, true} {
			if got := renderANSI(doc, false, byChar); !strings.Contains(got, want) {
				t.Errorf("byChar=%v: %q rendered %q, want %q", byChar, doc, got, want)
			}
		}
	}
}

// In reasoning a single "*" is arithmetic as often as emphasis, so it stays
// text there; bold stays bold, being how reasoning marks its headings. The
// answer parser is unchanged (TestANSIAsteriskEmphasisStillWorks).
func TestReasoningKeepsSingleStars(t *testing.T) {
	for _, tc := range []struct{ doc, text string }{
		{"set canvas.width = COLS*CELL*dpr etc.\n", "COLS*CELL*dpr"},
		{"a *b* c\n", "a *b* c"},
		{"**Planning** the layout\n", "Planning"},
		{"**bold *inner* bold**\n", "*inner*"},
	} {
		for _, byChar := range []bool{false, true} {
			var sb strings.Builder
			p := NewParser(NewANSI(&sb, true, DefaultTheme(), testRuleWidth)).NoStarItalics()
			if byChar {
				for _, c := range tc.doc {
					p.Write(string(c))
				}
			} else {
				p.Write(tc.doc)
			}
			p.End()
			got := sb.String()
			plain := sgrRe.ReplaceAllString(got, "")
			if strings.Contains(got, "\x1b[3m") || !strings.Contains(plain, tc.text) {
				t.Errorf("byChar=%v: %q rendered %q, want %q literal and no italics", byChar, tc.doc, got, tc.text)
			}
			if strings.HasPrefix(tc.doc, "**") && !strings.Contains(got, "\x1b[1m") {
				t.Errorf("byChar=%v: %q lost its bold: %q", byChar, tc.doc, got)
			}
		}
	}
}

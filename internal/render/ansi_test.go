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
		strings.Repeat("─", 40),
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
		if !strings.Contains(sb.String(), strings.Repeat("─", tc.want)) {
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

// TestANSIUnderscoreInWord confirms an intraword underscore (common in
// filenames and identifiers) is literal, not emphasis — CommonMark forbids
// intraword "_" emphasis. byChar exercises the fix across chunk boundaries.
func TestANSIUnderscoreInWord(t *testing.T) {
	// Note: __init__ is deliberately absent — its underscores are at word
	// boundaries, so CommonMark renders it as bold "init" (a known gotcha).
	// The rule only blocks underscores *between* word characters.
	for _, doc := range []string{
		"ansi_text.go\n",
		"a_b_c\n",
		"snake_case_name\n",
		"path/to/my_file_v2.py\n",
		"var_1 x_2\n",
	} {
		want := strings.TrimRight(doc, "\n")
		if got := renderANSI(doc, false, false); !strings.Contains(got, want) {
			t.Errorf("whole: %q rendered %q, want the underscores kept literal", doc, got)
		}
		if got := renderANSI(doc, false, true); !strings.Contains(got, want) {
			t.Errorf("by char: %q rendered %q, want the underscores kept literal", doc, got)
		}
	}
}

// TestANSIEmphasisStillWorks confirms boundary underscores and all asterisk
// emphasis (including intraword "*", which CommonMark allows) still render.
// color is on so the italic ("3") / bold ("1") SGR is observable.
func TestANSIEmphasisStillWorks(t *testing.T) {
	cases := map[string]string{
		"_em_\n":      "\x1b[3m", // underscore italic at a word boundary
		"foo _bar_\n": "\x1b[3m", // underscore italic after a space
		"a *b* c\n":   "\x1b[3m", // asterisk italic
		"x*y*z\n":     "\x1b[3m", // intraword asterisk IS emphasis
		"__b__\n":     "\x1b[1m", // underscore bold at a boundary
	}
	for doc, want := range cases {
		if got := renderANSI(doc, true, false); !strings.Contains(got, want) {
			t.Errorf("%q rendered %q, want emphasis SGR %q", doc, got, want)
		}
	}
}

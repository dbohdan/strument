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

func renderANSI(doc string, color bool, byChar bool) string {
	var sb strings.Builder
	p := NewParser(NewANSI(&sb, color))
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
	if !strings.Contains(color, "\x1b[1m") || !strings.Contains(color, "\x1b[36m") {
		t.Error("expected bold and cyan styles in color output")
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

func TestANSICheckbox(t *testing.T) {
	got := renderANSI("- [x] done\n- [ ] todo\n", false, false)
	if !strings.Contains(got, "[x] done") || !strings.Contains(got, "[ ] todo") {
		t.Errorf("checkbox output wrong:\n%q", got)
	}
}

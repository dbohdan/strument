// What a fetched page's text and tables look like to the model reading them.

package webpage

import (
	"strings"
	"testing"
)

// A data table is a Markdown table, one row to a line, under its own caption.
// Before, each row ran into the next on one line, and once tables were
// converted the caption came after its table, where it headed the next one.
func TestTablesKeepTheirRowsAndCaptions(t *testing.T) {
	const page = `<html><body>
<table><caption><b>J, L, S, T, Z kicks</b></caption>
<tr><th></th><th>Test 1</th><th>Test 2</th></tr>
<tr><td>0-&gt;R</td><td><tt>( 0, 0)</tt></td><td><tt>(-1, 0)</tt></td></tr>
<tr><td>R-&gt;0</td><td><tt>( 0, 0)</tt></td><td><tt>(+1, 0)</tt></td></tr>
</table>
<table><caption><b>I kicks</b></caption>
<tr><th></th><th>Test 1</th><th>Test 2</th></tr>
<tr><td>0-&gt;R</td><td><tt>( 0, 0)</tt></td><td><tt>(-2, 0)</tt></td></tr>
</table>
</body></html>`
	md := htmlToMarkdown(page, "https://tetris.example/SRS", Options{})
	for _, row := range []string{
		"| 0->R | `( 0, 0)` | `(-1, 0)` |",
		"| R->0 | `( 0, 0)` | `(+1, 0)` |",
		"| 0->R | `( 0, 0)` | `(-2, 0)` |",
	} {
		if !strings.Contains(md, row+"\n") && !strings.HasSuffix(md, row) {
			t.Errorf("missing the row %q on a line of its own:\n%s", row, md)
		}
	}
	jlstz, i := strings.Index(md, "J, L, S, T, Z kicks"), strings.Index(md, "I kicks")
	first, second := strings.Index(md, "`(-1, 0)`"), strings.Index(md, "`(-2, 0)`")
	if jlstz < 0 || i < 0 || jlstz >= first || first >= i || i >= second {
		t.Errorf("each caption should come right before its own table (at %d, %d, %d, %d):\n%s",
			jlstz, first, i, second, md)
	}
}

// "<" and ">" in text reach the model as themselves; a page that spells out
// an entity still says the entity.
func TestAngleBracketsInTextAreNotEscaped(t *testing.T) {
	const page = `<html><body>
<p>Rotate 0-&gt;R when a &lt; b, as in List&lt;T&gt;.</p>
<p>Write <code>a &lt;- b</code> in R.</p>
<p>The entity for less-than is &amp;lt;.</p>
</body></html>`
	md := htmlToMarkdown(page, "https://docs.example/x", Options{})
	for _, want := range []string{"Rotate 0->R when a < b, as in List<T>.", "`a <- b`", "less-than is &lt;."} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "&gt;") || strings.ContainsRune(md, '﷐') || strings.ContainsRune(md, '﷑') {
		t.Errorf("an escape or a placeholder leaked:\n%s", md)
	}
}

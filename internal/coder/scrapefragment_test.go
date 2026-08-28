// Reading part of a page: the fragment a URL already carries, and the outline
// that says which fragments there are.

package coder

import (
	"strings"
	"testing"
)

// The three shapes a fragment lands on, in the markup three real generators
// produce. Each was checked against the live site before being written down.
const (
	// MediaWiki wraps the heading with an edit link, so the prose is a sibling
	// of the *wrapper*, not of the heading that carries the id.
	wikiPage = `<html><body>
<h1 id="ENIAC">ENIAC</h1>
<p>Intro prose.</p>
<div class="mw-heading mw-heading2"><h2 id="Programming">Programming</h2><span class="mw-editsection">[edit]</span></div>
<p>ENIAC could be programmed to perform complex sequences.</p>
<p>Several language systems were developed.</p>
<div class="mw-heading mw-heading2"><h2 id="Legacy">Legacy</h2></div>
<p>Not part of Programming.</p>
</body></html>`

	// Sphinx nests the other way: the id is on the section that *contains* the
	// heading and everything under it. A rule that always climbed would break
	// this one, which is why the choice is made by measuring the result.
	sphinxPage = `<html><body>
<section id="built-in-types"><h1>Built-in Types</h1>
<p>Top prose.</p>
<section id="string-methods"><h2>String Methods</h2>
<p>Strings implement all of the common sequence operations.</p>
<dl><dt id="str.join">str.join(iterable)</dt><dd>Return a string which is the concatenation.</dd></dl>
</section>
<section id="set-types"><h2>Set Types</h2><p>Not part of String Methods.</p></section>
</section>
</body></html>`
)

func TestFragmentTakesTheSectionPastAHeadingWrapper(t *testing.T) {
	md := htmlToMarkdown(wikiPage, "https://en.wikipedia.org/wiki/ENIAC#Programming", ScrapeOptions{})

	if !strings.Contains(md, "complex sequences") || !strings.Contains(md, "Several language systems") {
		t.Errorf("the section's prose was not returned:\n%s", md)
	}
	if strings.Contains(md, "Intro prose") {
		t.Errorf("content before the section came along:\n%s", md)
	}
	if strings.Contains(md, "Not part of Programming") {
		t.Errorf("the section ran past the next heading of its rank:\n%s", md)
	}
}

func TestFragmentOnAContainerTakesItsSubtree(t *testing.T) {
	md := htmlToMarkdown(sphinxPage, "https://docs.python.org/3/library/stdtypes.html#string-methods", ScrapeOptions{})

	if !strings.Contains(md, "common sequence operations") {
		t.Errorf("the section body was not returned:\n%s", md)
	}
	if strings.Contains(md, "Top prose") || strings.Contains(md, "Not part of String Methods") {
		t.Errorf("the section was not bounded:\n%s", md)
	}
}

// A definition term brings its definition. Without this, #str.join returns the
// name of the method and not what it does.
func TestFragmentOnADefinitionTermTakesItsDefinition(t *testing.T) {
	md := htmlToMarkdown(sphinxPage, "https://docs.python.org/3/library/stdtypes.html#str.join", ScrapeOptions{})

	if !strings.Contains(md, "concatenation") {
		t.Errorf("the definition did not come with the term:\n%s", md)
	}
	if strings.Contains(md, "Set Types") {
		t.Errorf("more than the definition came back:\n%s", md)
	}
}

// A fragment that is not there returns the outline, not the page.
//
// Returning the page meant a truncated prefix — on Node's fs docs, 58 KB of
// mostly table-of-contents — handed to a model that had just said which section
// it wanted. Three of six models in a live pass raised this without being
// asked, one calling it "the least helpful thing possible". The anchors that do
// exist are smaller and are the only answer the model can act on.
func TestMissingFragmentReturnsTheOutline(t *testing.T) {
	md := htmlToMarkdown(sphinxPage, "https://docs.python.org/3/library/stdtypes.html#nope", ScrapeOptions{})

	if !strings.Contains(md, "no \"#nope\" section") {
		t.Errorf("a missing fragment was not reported:\n%s", md)
	}
	if !strings.Contains(md, "#string-methods") {
		t.Errorf("the anchors that do exist were not offered:\n%s", md)
	}
	if strings.Contains(md, "common sequence operations") {
		t.Errorf("the page's content came back instead of its map:\n%s", md)
	}
}

// Each section says how big it is, so a model can tell before fetching whether
// one result will hold it. Asked for by a model in the live pass, in as many
// words.
func TestOutlineReportsSectionSizes(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body><h1 id=\"top\">Top</h1><p>x</p>")
	b.WriteString("<h2 id=\"big\">Big</h2><p>" + strings.Repeat("word ", 2000) + "</p>")
	b.WriteString("<h2 id=\"small\">Small</h2><p>tiny</p></body></html>")

	outline := htmlToMarkdown(b.String(), "https://example.com/p", ScrapeOptions{Outline: true})

	if !strings.Contains(outline, "#small  (<1 KB)") {
		t.Errorf("a small section was not marked as small:\n%s", outline)
	}
	if !strings.Contains(outline, "#big  (10 KB)") {
		t.Errorf("a large section's size is wrong or missing:\n%s", outline)
	}
}

func TestOutlineListsSectionsWithTheirAnchors(t *testing.T) {
	md := htmlToMarkdown(sphinxPage, "https://docs.python.org/3/library/stdtypes.html", ScrapeOptions{Outline: true})

	for _, want := range []string{
		"- Built-in Types  #built-in-types",
		"  - String Methods  #string-methods",
		"  - Set Types  #set-types",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("outline missing %q:\n%s", want, md)
		}
	}
	// It is a map, not the territory.
	if strings.Contains(md, "common sequence operations") {
		t.Errorf("the outline carried the page's content:\n%s", md)
	}
	if !strings.Contains(md, "adding its anchor") {
		t.Errorf("the outline does not say how to use an anchor:\n%s", md)
	}
}

// An outline of a page whose headings have no anchors says so rather than
// listing sections nobody can fetch.
func TestOutlineSaysWhenNothingCanBeFetched(t *testing.T) {
	page := `<html><body><h1>Title</h1><p>x</p><h2>Part</h2><p>y</p></body></html>`
	md := htmlToMarkdown(page, "https://example.com/p", ScrapeOptions{Outline: true})

	if !strings.Contains(md, "no section of this page can be fetched") {
		t.Errorf("the outline promised navigation it cannot deliver:\n%s", md)
	}
	if !strings.Contains(md, "- Title") {
		t.Errorf("the headings themselves are still worth listing:\n%s", md)
	}
}

func TestOutlineOfAPageWithNoHeadings(t *testing.T) {
	md := htmlToMarkdown(`<html><body><p>Just prose.</p></body></html>`, "https://example.com/p", ScrapeOptions{Outline: true})
	if !strings.Contains(md, "no headings") {
		t.Errorf("got %q", md)
	}
}

// Fragments survive into the body text as Pandoc heading attributes, which is
// what lets a truncated page carry its own map without the HTML in hand.
func TestHeadingsCarryTheirAnchors(t *testing.T) {
	md := htmlToMarkdown(sphinxPage, "https://docs.python.org/3/library/stdtypes.html", ScrapeOptions{})
	if !strings.Contains(md, "{#string-methods}") {
		t.Errorf("headings lost their anchors:\n%s", md)
	}
}

// The shapes a sweep across fourteen real documentation sites turned up. Each
// of these is one generator's way of marking an anchor, and each was a bug
// found by fetching the real page — none by reasoning about the code.
func TestFragmentShapesFromRealGenerators(t *testing.T) {
	for _, tc := range []struct {
		name, html, frag, want, notWant string
	}{
		{
			// Node's API docs and the Lua manual both hang the id on an <a>
			// inside the heading. Taking that element literally returns the
			// anchor and nothing else.
			name: "id on an anchor inside the heading",
			html: `<h2>fs.opendirSync(path)<a class="mark" id="fs_opendirsync"></a></h2>
<p>Synchronously open a directory.</p><h2>next</h2><p>other</p>`,
			frag: "fs_opendirsync", want: "Synchronously open a directory", notWant: "other",
		},
		{
			name: "a name= instead of id, as hand-written HTML marks a target",
			html: `<h2><a name="pdf-print">lua_print</a></h2><p>Prints a value.</p><h2>x</h2><p>other</p>`,
			frag: "pdf-print", want: "Prints a value", notWant: "other",
		},
		{
			// Javadoc lays every member out as a list item, so the headings
			// convert to plain text and the page loses its structure.
			name: "heading inside a list item",
			html: `<ul class="member-list"><li><section class="detail">
<h3 id="getFirst()">getFirst()</h3><p>Returns the first element.</p></section></li>
<li><section class="detail"><h3 id="getLast()">getLast()</h3><p>Returns the last.</p></section></li></ul>`,
			frag: "getFirst()", want: "Returns the first element", notWant: "Returns the last",
		},
		{
			// ExDoc's ids carry a slash; javadoc's carry parentheses and
			// commas. An allowlist that rewrote them produced anchors that
			// looked right and matched nothing.
			name: "punctuation in the id",
			// The prose keeps its own markdown escaping — "min\_by" is correct
			// there — so the assertion is on text without punctuation to
			// escape. Only the anchor had to come through clean.
			html: `<h3 id="min_by/4-examples">Examples</h3><p>Returns the minimum element.</p><h3 id="z">z</h3><p>other</p>`,
			frag: "min_by/4-examples", want: "Returns the minimum element", notWant: "other",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := htmlToMarkdown("<html><body>"+tc.html+"</body></html>", "https://example.com/p#"+tc.frag, ScrapeOptions{})
			if !strings.Contains(md, tc.want) {
				t.Errorf("section did not contain %q:\n%s", tc.want, md)
			}
			if strings.Contains(md, tc.notWant) {
				t.Errorf("section ran past its end and included %q:\n%s", tc.notWant, md)
			}
		})
	}
}

// The Lua manual anchors all 428 of its headings with <a name>, and listed
// none of them until the outline learned to look there — which is a different
// code path from resolving a fragment, and was the one a mutation walked
// through untouched.
func TestOutlineFindsAnchorsWrittenAsNames(t *testing.T) {
	page := `<html><body><h2><a name="2.1">2.1</a> – Values and Types</h2><p>x</p>
<h3><a name="pdf-print">print</a></h3><p>y</p></body></html>`

	outline := htmlToMarkdown(page, "https://www.lua.org/manual/5.4/manual.html", ScrapeOptions{Outline: true})

	for _, want := range []string{"#2.1", "#pdf-print"} {
		if !strings.Contains(outline, want) {
			t.Errorf("outline missing %q:\n%s", want, outline)
		}
	}
	if strings.Contains(outline, "no section of this page can be fetched") {
		t.Errorf("the outline gave up on a page whose headings are all anchored:\n%s", outline)
	}
}

// The anchors an outline advertises have to be the anchors the page has. The
// converter escapes markdown punctuation in heading text, the {#anchor} along
// with it, so "#Operator_precedence" was published as "#Operator\_precedence" —
// four of fourteen sites advertising fragments that could not match, which is
// worse than advertising none.
func TestOutlineAnchorsAreNotEscaped(t *testing.T) {
	page := `<html><body><h2 id="Operator_precedence">Operator precedence</h2><p>x</p>
<ul class="member-list"><li><section><h3 id="add(int,E)">add</h3><p>y</p></section></li></ul></body></html>`

	outline := htmlToMarkdown(page, "https://go.dev/ref/spec", ScrapeOptions{Outline: true})
	body := htmlToMarkdown(page, "https://go.dev/ref/spec", ScrapeOptions{})

	for _, s := range []string{outline, body} {
		if strings.Contains(s, `\_`) || strings.Contains(s, `\(`) {
			t.Errorf("an anchor was published with escapes in it:\n%s", s)
		}
	}
	for _, want := range []string{"#Operator_precedence", "#add(int,E)"} {
		if !strings.Contains(outline, want) {
			t.Errorf("outline missing %q:\n%s", want, outline)
		}
	}
	// And the anchors it advertises actually resolve.
	for _, frag := range []string{"Operator_precedence", "add(int,E)"} {
		sec := htmlToMarkdown(page, "https://go.dev/ref/spec#"+frag, ScrapeOptions{})
		if strings.Contains(sec, "so the whole page follows") {
			t.Errorf("the outline advertised #%s, which does not resolve", frag)
		}
	}
}

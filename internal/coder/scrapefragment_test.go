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

// A fragment that is not there returns the page and says so. Silently returning
// the whole page would leave a model that asked for a section unable to tell
// which of the two it is holding.
func TestMissingFragmentSaysSo(t *testing.T) {
	md := htmlToMarkdown(sphinxPage, "https://docs.python.org/3/library/stdtypes.html#nope", ScrapeOptions{})

	if !strings.Contains(md, "no \"#nope\" section") {
		t.Errorf("a missing fragment was not reported:\n%s", md)
	}
	if !strings.Contains(md, "Top prose") {
		t.Errorf("the fallback did not include the page:\n%s", md)
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

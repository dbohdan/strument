// Link handling in a fetched page: what the model can act on, and what it
// cannot and so should not be handed.

package coder

import (
	"fmt"
	"strings"
	"testing"
)

const linkPage = `<html><body>
<div class="related" role="navigation"><a href="../genindex.html">index</a> | <a href="constants.html">previous</a></div>
<h1>Built-in Types<a class="headerlink" href="#built-in-types" title="Link to this heading">¶</a></h1>
<p>See <a href="exceptions.html" title="Built-in Exceptions">exceptions</a> and
<a href="../tutorial/index.html">the tutorial</a> and
<a href="https://peps.python.org/pep-0008/">PEP 8</a>.</p>
<p>Jump to <a href="#id12">note 1</a> or <a href="stdtypes.html#str">str</a>.</p>
<p>Mail <a href="mailto:x@example.com">us</a> or <a href="javascript:void(0)">click</a>.</p>
<footer role="contentinfo">Copyright 2026.</footer>
</body></html>`

const pageURL = "https://docs.python.org/3/library/stdtypes.html"

func TestFetchedLinksBecomeAbsolute(t *testing.T) {
	md := htmlToMarkdown(linkPage, pageURL, ScrapeOptions{})

	// A relative href is a link the model will try and fail on — against
	// Strument's own "the URL needs a scheme" refusal, which is a confusing
	// thing to hit on a link the harness handed over.
	for _, want := range []string{
		"https://docs.python.org/3/library/exceptions.html",
		"https://docs.python.org/3/tutorial/index.html",
		"https://peps.python.org/pep-0008/", // already absolute, left alone
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing resolved link %s in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "](exceptions.html") || strings.Contains(md, "](../tutorial") {
		t.Errorf("a relative link survived:\n%s", md)
	}
}

// A link to this very page keeps its words and loses its destination: there is
// nothing to fetch at "#id12", and re-fetching the page you are reading is the
// one thing the link cannot be for.
func TestSamePageAnchorsKeepTheirText(t *testing.T) {
	md := htmlToMarkdown(linkPage, pageURL, ScrapeOptions{})

	if !strings.Contains(md, "note 1") || !strings.Contains(md, "str") {
		t.Errorf("anchor text was dropped along with the anchor:\n%s", md)
	}
	if strings.Contains(md, "](#id12)") || strings.Contains(md, "#str)") {
		t.Errorf("a same-page anchor kept a destination:\n%s", md)
	}
}

func TestUnfetchableSchemesLoseTheirHref(t *testing.T) {
	md := htmlToMarkdown(linkPage, pageURL, ScrapeOptions{})
	for _, bad := range []string{"mailto:", "javascript:"} {
		if strings.Contains(md, bad) {
			t.Errorf("%s survived as a link:\n%s", bad, md)
		}
	}
}

// The ¶ beside every heading is chrome three generators emit, and the
// navigation bars are chrome Sphinx marks with a role rather than a <nav>.
func TestPermalinksAndAriaChromeAreDropped(t *testing.T) {
	md := htmlToMarkdown(linkPage, pageURL, ScrapeOptions{})

	if strings.Contains(md, "¶") {
		t.Errorf("a permalink anchor survived:\n%s", md)
	}
	for _, chrome := range []string{"genindex", "previous", "Copyright 2026"} {
		if strings.Contains(md, chrome) {
			t.Errorf("ARIA-marked chrome %q survived:\n%s", chrome, md)
		}
	}
	// The counter-half: the heading and the prose it labels are still there.
	if !strings.Contains(md, "Built-in Types") || !strings.Contains(md, "PEP 8") {
		t.Errorf("real content was removed with the chrome:\n%s", md)
	}
}

// A page with no usable base URL must still convert rather than fail.
func TestConversionSurvivesABadPageURL(t *testing.T) {
	if md := htmlToMarkdown(linkPage, "::not a url::", ScrapeOptions{}); !strings.Contains(md, "Built-in Types") {
		t.Errorf("a bad page URL lost the content:\n%s", md)
	}
}

// A cut page carries its own map. The note it replaces said to "fetch a more
// specific page", which assumes a page that may not exist and asks the model to
// find it while holding a quarter of the one it has; the predictable next move
// is to abandon the tool for curl.
func TestFetchTruncationCarriesTheOutline(t *testing.T) {
	short := "a short page"
	if got := truncateFetch(short, "https://example.com/p"); got != short {
		t.Errorf("a page under the cap was altered: %q", got)
	}

	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "## Section %d {#sec-%d}\n\n%s\n\n", i, i, strings.Repeat("x", 4000))
	}
	got := truncateFetch(b.String(), "https://example.com/p")

	// The result respects the cap it exists to enforce. An earlier version
	// appended the note past it, which is the bug this pins.
	if len(got) > maxToolOutputBytes {
		t.Errorf("the truncated result is %d bytes, over the %d cap", len(got), maxToolOutputBytes)
	}
	// The warning is at the top, where it is read before the content rather
	// than after 60 KB of it — a model that searches a partial page believing
	// it whole reports the string absent, which is a wrong answer, not a slow
	// one.
	if head, _, _ := strings.Cut(got, "\n"); !strings.Contains(head, "Partial page") {
		t.Errorf("the result does not announce itself as partial on its first line: %q", head)
	}
	for _, want := range []string{"Partial page", "KB", "adding its anchor", "#sec-0", "#sec-39"} {
		if !strings.Contains(got, want) {
			t.Errorf("the cut page does not carry %q", want)
		}
	}
	// The map covers the whole page, not just the part that survived the cut —
	// which is the point of computing it before truncating.
	if !strings.Contains(got, "Section 39") {
		t.Error("the outline stops where the content was cut, so it maps nothing new")
	}
}

// Far more page than one result can carry: send the map, not a prefix of it.
//
// Four of six models with an order-independent preference chose this over a
// prefix in a blind pairwise test, and the reason they gave was the same one
// each time — on a page this size the prefix is the navigation sidebar and the
// table of contents, and the outline of the same headings is appended below it
// anyway, so the result carries the same list twice and the useful copy costs
// 38 KB to reach.
func TestVeryLargePageAnswersWithItsOutline(t *testing.T) {
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, "## Section %d {#sec-%d}\n\n%s\n\n", i, i, strings.Repeat("x", 5000))
	}
	got := truncateFetch(b.String(), "https://example.com/p")

	if !strings.Contains(got, "times what one tool result carries") {
		t.Errorf("a page far over the cap did not answer with its outline:\n%s", got[:200])
	}
	if !strings.Contains(got, "#sec-59") {
		t.Error("the map does not cover the whole page")
	}
	if strings.Contains(got, strings.Repeat("x", 200)) {
		t.Error("the prefix came along with the map")
	}
	if len(got) > maxToolOutputBytes {
		t.Errorf("result is %d bytes, over the %d cap", len(got), maxToolOutputBytes)
	}
}

// Just over the cap keeps the prefix: it is most of the page, and trading it
// for a map would buy a guaranteed extra round trip for content already in
// hand. The ratio is what decides, not the excess.
func TestBarelyOversizedPageKeepsItsContent(t *testing.T) {
	var b strings.Builder
	for i := range 12 {
		fmt.Fprintf(&b, "## Section %d {#sec-%d}\n\n%s\n\n", i, i, strings.Repeat("x", 5000))
	}
	got := truncateFetch(b.String(), "https://example.com/p")

	if !strings.Contains(got, "Partial page") {
		t.Errorf("a page just over the cap did not return a partial page:\n%s", got[:200])
	}
	if !strings.Contains(got, strings.Repeat("x", 200)) {
		t.Error("the content was dropped from a page that is mostly deliverable")
	}
	if !strings.Contains(got, "#sec-11") {
		t.Error("the partial page did not carry its map")
	}
}

// A page whose map would crowd out its content keeps the map: it is the more
// useful half, and the content is what the anchors are for.
func TestFetchTruncationKeepsTheMapOnAHeadingHeavyPage(t *testing.T) {
	var b strings.Builder
	for i := range 4000 {
		fmt.Fprintf(&b, "### Item %d {#item-%d}\n\ntext\n\n", i, i)
	}
	got := truncateFetch(b.String(), "https://example.com/p")

	if len(got) > maxToolOutputBytes {
		t.Errorf("result is %d bytes, over the %d cap", len(got), maxToolOutputBytes)
	}
	if !strings.Contains(got, "Partial page") {
		t.Error("no note on a page that was cut")
	}
}

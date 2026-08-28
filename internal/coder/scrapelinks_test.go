// Link handling in a fetched page: what the model can act on, and what it
// cannot and so should not be handed.

package coder

import (
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
	md := htmlToMarkdown(linkPage, pageURL)

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
	md := htmlToMarkdown(linkPage, pageURL)

	if !strings.Contains(md, "note 1") || !strings.Contains(md, "str") {
		t.Errorf("anchor text was dropped along with the anchor:\n%s", md)
	}
	if strings.Contains(md, "](#id12)") || strings.Contains(md, "#str)") {
		t.Errorf("a same-page anchor kept a destination:\n%s", md)
	}
}

func TestUnfetchableSchemesLoseTheirHref(t *testing.T) {
	md := htmlToMarkdown(linkPage, pageURL)
	for _, bad := range []string{"mailto:", "javascript:"} {
		if strings.Contains(md, bad) {
			t.Errorf("%s survived as a link:\n%s", bad, md)
		}
	}
}

// The ¶ beside every heading is chrome three generators emit, and the
// navigation bars are chrome Sphinx marks with a role rather than a <nav>.
func TestPermalinksAndAriaChromeAreDropped(t *testing.T) {
	md := htmlToMarkdown(linkPage, pageURL)

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
	if md := htmlToMarkdown(linkPage, "::not a url::"); !strings.Contains(md, "Built-in Types") {
		t.Errorf("a bad page URL lost the content:\n%s", md)
	}
}

// The truncation note says how much there was and what to do instead, because
// the generic one leaves the model unable to tell a short page from a cut one.
func TestFetchTruncationSaysWhatToDo(t *testing.T) {
	short := "a short page"
	if got := truncateFetch(short); got != short {
		t.Errorf("a page under the cap was altered: %q", got)
	}

	long := strings.Repeat("x", maxToolOutputBytes*4)
	got := truncateFetch(long)
	if len(got) <= maxToolOutputBytes {
		t.Fatal("nothing was returned above the cap")
	}
	for _, want := range []string{"Cut off here", "KB", "more specific page", "same prefix"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note does not mention %q: %q", want, got[maxToolOutputBytes:])
		}
	}
}

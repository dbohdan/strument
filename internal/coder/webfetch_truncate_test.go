package coder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/webpage"
)

// A cut page carries its own map. The note it replaces said to "fetch a more
// specific page", which assumes a page that may not exist and asks the model to
// find it while holding a quarter of the one it has; the predictable next move
// is to abandon the tool for curl.
func TestFetchTruncationCarriesTheOutline(t *testing.T) {
	short := "a short page"
	if got := truncateFetch(short, "https://example.com/p", webpage.KindHTML); got != short {
		t.Errorf("a page under the cap was altered: %q", got)
	}

	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "## Section %d {#sec-%d}\n\n%s\n\n", i, i, strings.Repeat("x", 4000))
	}
	got := truncateFetch(b.String(), "https://example.com/p", webpage.KindHTML)

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
	got := truncateFetch(b.String(), "https://example.com/p", webpage.KindHTML)

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
	got := truncateFetch(b.String(), "https://example.com/p", webpage.KindHTML)

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
	got := truncateFetch(b.String(), "https://example.com/p", webpage.KindHTML)

	if len(got) > maxToolOutputBytes {
		t.Errorf("result is %d bytes, over the %d cap", len(got), maxToolOutputBytes)
	}
	if !strings.Contains(got, "Partial page") {
		t.Error("no note on a page that was cut")
	}
}

// P1: an oversized text page that arrives without outline:true must still get
// a kind-aware map — the old path ran the HTML heading scanner over it and
// answered "This page has no headings", with navigation advice (URL anchors)
// that a text page cannot act on.
func TestTruncatedCodePageCarriesADefOutline(t *testing.T) {
	var b strings.Builder
	b.WriteString("package interp\n\n")
	// Enough bulk to cross the 4x outline-switch threshold, but few enough
	// definitions for the outline to fit its own budget: wide functions, not
	// many of them. This walks the map-only path — the one a model is most
	// likely to meet without outline:true.
	for i := range 40 {
		fmt.Fprintf(&b, "func handler%d(w *Writer) {\n", i)
		b.WriteString(strings.Repeat("\twork(step)\n", 400))
		fmt.Fprintf(&b, "\t_ = w\n}\n\n")
	}
	wrapped := webpage.Wrap("https://raw.githubusercontent.com/x/y/v1/interp.go", b.String())
	kind := webpage.Classify("https://raw.githubusercontent.com/x/y/v1/interp.go", b.String())
	got := truncateFetch(wrapped, "https://raw.githubusercontent.com/x/y/v1/interp.go", kind)

	if strings.Contains(got, "no headings") {
		t.Errorf("the text page got the HTML scanner's no-headings answer:\n%s", got[:300])
	}
	// The line numbers must be the file's, not the wrapped text's: the
	// framing line added two lines, and a range computed over the wrapped
	// text would fetch the wrong lines.
	if !strings.Contains(got, "lines 1-1") && !strings.Contains(got, "lines 5-7") {
		t.Errorf("the def outline's line numbers are not the file's:\n%s", got[len(got)-600:])
	}
	if !strings.Contains(got, "function handler0") || !strings.Contains(got, "function handler39") {
		t.Errorf("the map does not cover the whole file:\n%s", got[len(got)-600:])
	}
}

// An HTML page too large to return is answered with an outline of its
// converted text, and that outline numbers lines. The range it recommends has
// to fetch those lines: refused, the advice sent a model round the same map
// three times (live, on OpenRouter's 613 KB provider-routing page).
func TestOversizedHTMLOutlineRangeFetchesItsSection(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body><h1 id=\"top\">Guide</h1>")
	for i := range 400 {
		fmt.Fprintf(&b, "<h2 id=\"s%d\">Section %d</h2><p>%s marker-%d</p>", i, i, strings.Repeat("filler ", 120), i)
	}
	b.WriteString("</body></html>")
	page := b.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(srv.Close)
	u := srv.URL + "/guide"
	fetch := webpage.NewSimple(nil, "")

	content, err := fetch(context.Background(), u, webpage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	res := truncateFetch(content, u, webpage.Classify(u, webpage.Unwrap(content)))
	m := regexp.MustCompile(`Section 250 \{#s250\}\s+\(lines? (\d+)(?:-(\d+))?\)`).FindStringSubmatch(res)
	if m == nil {
		t.Fatalf("the oversized page's outline does not number section 250:\n%.2000s", res)
	}
	lo, _ := strconv.Atoi(m[1])

	part, err := fetch(context.Background(), u, webpage.Options{Range: fmt.Sprintf("%d-%d", lo, lo+3)})
	if err != nil {
		t.Fatal(err)
	}
	if body := webpage.Unwrap(part); !strings.HasPrefix(body, "## Section 250") || !strings.Contains(body, "marker-250") {
		t.Errorf("the range the outline gives did not fetch its section:\n%s", body)
	}
}

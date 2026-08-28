package coder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
)

const (
	scrapeUserAgentDefault = "Strument"
	scrapeAccept           = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	scrapeMaxBytes         = 2 * 1024 * 1024
)

var (
	blankRunRe = regexp.MustCompile(`\n{3,}`)
	// Anchors that wrapped only an icon/logo image become empty [](url) links
	// once the image is dropped; strip those artifacts, keeping real links.
	emptyLinkRe = regexp.MustCompile(`\[\s*\]\([^)]*\)`)
)

// SimpleScraper fetches a URL and reduces HTML to markdown, with no proxy and a
// default user-agent — the standalone stand-in for aider's scraper.
var SimpleScraper = NewSimpleScraper(nil, "")

// NewSimpleScraper returns a Scraper that fetches over the given transport
// (nil => the default) and identifies itself with userAgent ("" => "Strument").
// The global proxy is threaded via the transport so scraping honors it like
// every other outbound HTTPS action. HTML becomes markdown (html-to-markdown,
// after slimming media); other content types are returned as text.
func NewSimpleScraper(transport http.RoundTripper, userAgent string) Scraper {
	if userAgent == "" {
		userAgent = scrapeUserAgentDefault
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	return func(ctx context.Context, url string, opts ScrapeOptions) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		// Identify the request: Go's default user-agent reads as a bot and is
		// widely blocked. HTTP-Referer/X-Title are OpenRouter-specific and have
		// no business on an arbitrary site, so they are not sent here.
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", scrapeAccept)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, scrapeMaxBytes))
		if err != nil {
			return "", err
		}

		text := string(body)
		if strings.Contains(resp.Header.Get("Content-Type"), "html") {
			text = htmlToMarkdown(text, url, opts)
		}
		// Where the content actually came from, which is not always where it
		// was asked for. The model picks the URL now, so a shortener or an
		// open redirect landing somewhere else is a thing the transcript should
		// record rather than something only the network saw.
		final := url
		if resp.Request != nil && resp.Request.URL != nil {
			final = resp.Request.URL.String()
		}
		wrap := wrapContent
		if opts.Outline {
			wrap = wrapOutline
		}
		if final != url {
			return wrap(url, text) + "\n\n(Redirected to " + final + ".)\n", nil
		}
		return wrap(url, text), nil
	}
}

// wrapContent frames a scraped page the way every scraper presents it to the
// model, so the phrasing stays identical across the HTTP and command paths.
func wrapContent(url, text string) string {
	return fmt.Sprintf("Here is the content of %s:\n\n%s", url, text)
}

// wrapOutline frames a map rather than a page. Two adjacent lines saying "the
// outline follows" and "here is the content of" contradict each other, and a
// reader calibrates on the framing line.
func wrapOutline(url, text string) string {
	return fmt.Sprintf("Here is the outline of %s:\n\n%s", url, text)
}

// buildScrapeArgs fills the URL into an argv template: every %s in an element is
// replaced, and if no element mentions %s the URL is appended as a final
// argument. Substituting into an argv slice (never a shell string) keeps a
// hostile URL from injecting extra arguments.
func buildScrapeArgs(argv []string, url string) []string {
	out := make([]string, 0, len(argv)+1)
	found := false
	for _, a := range argv {
		if strings.Contains(a, "%s") {
			found = true
			out = append(out, strings.ReplaceAll(a, "%s", url))
		} else {
			out = append(out, a)
		}
	}
	if !found {
		out = append(out, url)
	}
	return out
}

// boundedBuffer keeps at most max bytes and drops the rest, so a chatty
// subprocess can't exhaust memory through stdout or stderr. It always reports a
// full write so the child never blocks or sees a short-write error.
type boundedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

// NewCommandScraper returns a Scraper that fetches a URL by running an external
// command — typically a headless browser dumping the rendered DOM — and reduces
// its stdout, treated as HTML, to markdown through the same pipeline as the HTTP
// scraper. This is the opt-in path for JavaScript-rendered pages: it keeps
// Strument a single static binary and lets the user bring their own browser. The
// command runs without a shell, so a hostile URL cannot inject arguments; the
// global proxy does not apply here — the command manages its own networking.
// env supplies the environment per fetch: the caller passes the
// allowlist-filtered set, because the scraped page's contents reach the model's
// context — as a closure, so /env changes apply without rebuilding the scraper.
func NewCommandScraper(argv []string, timeout time.Duration, env func() []string) Scraper {
	return func(ctx context.Context, url string, opts ScrapeOptions) (string, error) {
		if len(argv) == 0 {
			return "", errors.New("scraper command is empty")
		}
		runCtx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		args := buildScrapeArgs(argv, url)
		// The command comes from the operator's config, and the URL is a single
		// argv element (never shell-interpreted), so this is not attacker-run.
		cmd := exec.CommandContext(runCtx, args[0], args[1:]...) //nolint:gosec
		if env != nil {
			cmd.Env = env()
		}

		stdout := &boundedBuffer{max: scrapeMaxBytes}
		stderr := &boundedBuffer{max: 4096}
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		err := cmd.Run()
		if runCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("scraper command timed out after %s", timeout)
		}
		if err != nil {
			if tail := strings.TrimSpace(stderr.buf.String()); tail != "" {
				return "", fmt.Errorf("scraper command failed: %w: %s", err, tail)
			}
			return "", fmt.Errorf("scraper command failed: %w", err)
		}
		if strings.TrimSpace(stdout.buf.String()) == "" {
			return "", errors.New("scraper command produced no output")
		}
		wrap := wrapContent
		if opts.Outline {
			wrap = wrapOutline
		}
		return wrap(url, htmlToMarkdown(stdout.buf.String(), url, opts)), nil
	}
}

// resolveLinks rewrites a page's links so the model can act on them, and drops
// the ones it cannot.
//
// This became worth doing the day webfetch landed. While a URL could only come
// from the user, a relative href was just cosmetic noise in the text; now the
// model can fetch, and `exceptions.html` is a link it will try and fail on —
// against Strument's own "the URL needs a scheme" refusal, which is a confusing
// thing to hit on a link the harness itself handed over.
//
// Same-page anchors are unwrapped to their text instead. There is nothing to
// fetch at "#id12", and on one Python library page there were 724 of them —
// more than the relative links and the permalinks combined.
//
// Permalink anchors go entirely: the ¶ beside every heading is chrome that
// Sphinx, MkDocs, and Docusaurus all emit, and it says nothing a reader or a
// model wants.
func resolveLinks(doc *goquery.Document, pageURL string) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return
	}
	doc.Find("a.headerlink, a.anchor, a.hash-link").Remove()
	// unlink keeps a link's words and drops the link itself. RemoveAttr is not
	// enough: <a>text</a> converts to "[text]()", an empty target that is
	// noisier than the href it replaced — and there were 724 same-page anchors
	// on one page to be noisy with.
	unlink := func(a *goquery.Selection) {
		if a.Contents().Length() == 0 {
			a.Remove()
			return
		}
		a.Contents().Unwrap()
	}
	doc.Find("a").Each(func(_ int, a *goquery.Selection) {
		if t := strings.TrimSpace(a.Text()); t == "¶" || t == "§" || t == "#" {
			a.Remove()
			return
		}
		href, ok := a.Attr("href")
		if !ok {
			return
		}
		ref, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			unlink(a)
			return
		}
		abs := base.ResolveReference(ref)
		// A link to this very page, fragment or not: keep the words, drop the
		// destination, since fetching it again is the one thing it cannot be
		// for.
		if abs.Scheme == base.Scheme && abs.Host == base.Host && abs.Path == base.Path && abs.RawQuery == base.RawQuery {
			unlink(a)
			return
		}
		if abs.Scheme != "http" && abs.Scheme != "https" {
			unlink(a) // mailto:, javascript:, data: — not fetchable
			return
		}
		a.SetAttr("href", abs.String())
	})
}

// headingLevel is 1-6 for a heading element and 0 for anything else.
func headingLevel(sel *goquery.Selection) int {
	switch goquery.NodeName(sel) {
	case "h1":
		return 1
	case "h2":
		return 2
	case "h3":
		return 3
	case "h4":
		return 4
	case "h5":
		return 5
	case "h6":
		return 6
	}
	return 0
}

// headingAnchorRe matches the {#anchor} a heading carries, escapes and all.
var headingAnchorRe = regexp.MustCompile(`\{#[^}\n]*\}`)

// sizeHint is a section's size, rounded to something a reader can act on.
func sizeHint(n int) string {
	if n < 1024 {
		return "(<1 KB)"
	}
	return fmt.Sprintf("(%d KB)", (n+512)/1024)
}

// mdLinkRe matches a markdown inline link, capturing its text.
var mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// safeAnchor removes only what would break the {#...} written around a
// fragment: the closing brace, a backslash, and whitespace, which HTML forbids
// in an id anyway.
//
// It was a conservative allowlist to begin with, and the allowlist was the bug.
// Javadoc's ids are "getFirst()" and "add(int,java.lang.Object)", ExDoc's are
// "min_by/4-examples"; rewriting that punctuation produced "getFirst--" and
// "min_by-4-examples" — anchors that look right and match nothing. Every
// character it dropped is legal in a URL fragment.
var safeAnchor = regexp.MustCompile(`[}\\\s\x00-\x1f]`)

// anchorOf finds the fragment a heading can be reached by.
//
// Three places, because three generators put it in three: on the heading
// (MediaWiki, pkg.go.dev), on the section that wraps it (Sphinx, which is why
// the Python docs' ids are not on their h2s at all), or on a span inside it
// (older Sphinx and MkDocs).
func anchorOf(h *goquery.Selection) string {
	if id, ok := h.Attr("id"); ok && id != "" {
		return id
	}
	if id, ok := h.Parent().Attr("id"); ok && id != "" {
		return id
	}
	id := ""
	h.Find("[id]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		id, _ = s.Attr("id")
		return id == ""
	})
	if id != "" {
		return id
	}
	// <a name> is how hand-written HTML marks a target, and the Lua manual —
	// 428 headings, every one of them anchored this way — is the reason this
	// is not a historical curiosity.
	h.Find("a[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		id, _ = s.Attr("name")
		return id == ""
	})
	return id
}

// unwrapHeadingLists lifts document sections out of the lists they are laid
// out in.
//
// A heading inside an <li> does not convert to a markdown heading — it cannot,
// since "#" inside a list item means something else — so it comes out as plain
// text and the page loses its structure. Javadoc puts every method's <section>
// inside an <li>: the ArrayList page has 100 members and converted to exactly
// one heading, its title, with all the content present and none of it findable.
//
// The rule is general rather than javadoc-shaped: a list whose items carry
// headings is a document being laid out as a list, so the list markup goes and
// the sections stay.
func unwrapHeadingLists(doc *goquery.Document) {
	for range 4 { // nested member lists; bounded so a cycle cannot hang this
		lists := doc.Find("ul, ol").FilterFunction(func(_ int, l *goquery.Selection) bool {
			return l.ChildrenFiltered("li").FilterFunction(func(_ int, li *goquery.Selection) bool {
				return li.Find("h1, h2, h3, h4, h5, h6").Length() > 0
			}).Length() > 0
		})
		if lists.Length() == 0 {
			return
		}
		lists.Each(func(_ int, l *goquery.Selection) {
			l.ChildrenFiltered("li").Each(func(_ int, li *goquery.Selection) {
				li.Contents().Unwrap()
			})
			l.Contents().Unwrap()
		})
	}
}

// markHeadings writes each heading's own fragment into its text, as Pandoc's
// heading-attribute syntax.
//
// It is what makes everything downstream a string operation: the outline is
// then the heading lines of the markdown, and the outline appended to a
// truncated page needs no second pass over HTML that is no longer in hand. It
// costs about fifteen characters per heading — six kilobytes on the largest
// page measured, against the two hundred and twenty-eight it makes navigable.
func markHeadings(doc *goquery.Document) {
	doc.Find("h1, h2, h3, h4, h5, h6").Each(func(_ int, h *goquery.Selection) {
		if id := anchorOf(h); id != "" {
			h.AppendHtml(" {#" + safeAnchor.ReplaceAllString(id, "-") + "}")
		}
	})
}

// collectSection emits a heading and everything that follows anchor until the
// next heading of the heading's own rank or higher. It returns the HTML and the
// length of its text, which is what tells the caller whether it found a section
// or just a heading.
func collectSection(heading, anchor *goquery.Selection, lvl int) (string, int) {
	var b strings.Builder
	text := 0
	add := func(s *goquery.Selection) {
		if h, err := goquery.OuterHtml(s); err == nil {
			b.WriteString(h)
			text += len(strings.TrimSpace(s.Text()))
		}
	}
	add(heading)
	for sib := anchor.Next(); sib.Length() > 0; sib = sib.Next() {
		if l := headingLevel(sib); l > 0 && l <= lvl {
			break
		}
		// A wrapper around the next heading of this rank ends the section too.
		if inner := sib.ChildrenFiltered("h1,h2,h3,h4,h5,h6"); inner.Length() > 0 {
			if l := headingLevel(inner.First()); l > 0 && l <= lvl {
				break
			}
		}
		add(sib)
	}
	return b.String(), text
}

// fragmentHTML narrows a page to the part a URL fragment names, which is the
// whole of what makes a 228 KB page usable through a 60 KB window.
//
// The idea is a link-preview bot's: a fragment already says which part of the
// page the reader meant, and a fetcher that ignores it throws that away and
// then apologizes for the size.
//
// What the fragment lands on differs by generator, so this handles the three
// shapes rather than one. A heading takes the siblings that follow it, up to
// the next heading of its own rank or higher. A container — Sphinx's
// <section>, an <article> — is already the section. A <dt> is a definition
// term, so its <dd> comes along, which is what makes docs.python.org's
// #str.join return the method rather than its name.
//
// MediaWiki needs the fallback in the middle: it wraps the heading in a div,
// so the content that follows are siblings of the *wrapper* rather than of the
// heading that carries the id.
func fragmentHTML(doc *goquery.Document, frag string) (html string, text int, ok bool) {
	var node *goquery.Selection
	doc.Find("[id]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if id, _ := s.Attr("id"); id == frag {
			node = s
			return false
		}
		return true
	})
	if node == nil {
		doc.Find("a[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			if n, _ := s.Attr("name"); n == frag {
				node = s
				return false
			}
			return true
		})
	}
	if node == nil || node.Length() == 0 {
		return "", 0, false
	}
	// An id on something inside a heading means the heading. Node's API docs
	// hang it on an <a class="mark"> in the heading and the Lua manual on an
	// <a name>; taking the element literally returned 87 and 107 bytes of bare
	// anchor, which is a section in neither case.
	if h := node.Closest("h1, h2, h3, h4, h5, h6"); h.Length() > 0 {
		node = h
	}
	// An anchor marker with no text of its own, sitting immediately before a
	// heading, means that heading. Sphinx renders an explicit `.. _label:` as
	// <span id="label"></span> inside the section and just before its <h2>, so
	// docs.python.org/3/library/string.html#formatstrings resolves to an empty
	// span — and the page's own cross-references link by exactly those labels,
	// so a model following an in-page link lands here rather than on the
	// section id. Taking the element literally returned a span that converts to
	// no text at all.
	if strings.TrimSpace(node.Text()) == "" {
		if next := node.Next(); headingLevel(next) > 0 {
			node = next
		}
	}

	var b strings.Builder
	add := func(s *goquery.Selection) {
		if h, err := goquery.OuterHtml(s); err == nil {
			b.WriteString(h)
		}
	}

	if lvl := headingLevel(node); lvl > 0 {
		// Collect from the heading, and if that yields the heading and nothing
		// else, collect from its wrapper instead.
		//
		// Judged by what came back rather than by predicting the markup, which
		// is what the first attempt did and got wrong: MediaWiki wraps the
		// heading in <div class="mw-heading"> together with an edit-link span,
		// so the heading *does* have a following sibling and a
		// structure-predicting test never fires — while the prose, the thing
		// actually wanted, is a sibling of the wrapper. Sphinx nests the other
		// way round, with the whole section body inside the element that
		// carries the id, so a rule that always climbed would break it. Trying
		// and measuring works on both.
		html, text := collectSection(node, node, lvl)
		if text <= len(strings.TrimSpace(node.Text()))+120 {
			if p := node.Parent(); p.Length() > 0 && !slices.Contains([]string{"body", "html", ""}, goquery.NodeName(p)) {
				if wider, widerText := collectSection(node, p, lvl); widerText > text {
					html, text = wider, widerText
				}
			}
		}
		return html, text, true
	}

	add(node)
	text = len(strings.TrimSpace(node.Text()))
	if goquery.NodeName(node) == "dt" {
		if dd := node.Next(); goquery.NodeName(dd) == "dd" {
			add(dd)
			text += len(strings.TrimSpace(dd.Text()))
		}
	}
	return b.String(), text, true
}

// outlineOf reduces converted markdown to its headings, indented by depth and
// each with the fragment it can be fetched by.
//
// A string operation rather than a second pass over the HTML, because
// markHeadings has already put the fragments where a line-scan can see them —
// which is what lets a truncated page carry its own map without the document
// still being in hand.
type outlineEntry struct {
	depth, start int
	text, anchor string
}

func outlineOf(md, pageURL string) string {
	var b strings.Builder
	var entries []outlineEntry
	anchored := 0
	total := 0
	offset := 0
	for line := range strings.SplitSeq(md, "\n") {
		lineStart := offset
		offset += len(line) + 1
		_ = lineStart
		hashes := len(line) - len(strings.TrimLeft(line, "#"))
		if hashes < 1 || hashes > 6 || !strings.HasPrefix(line[hashes:], " ") {
			continue
		}
		total++
		// A heading's own links are noise in a map: what the reader wants is
		// the words and the anchor. On the Python docs one heading carried
		// three absolute links to functions.html and ran to 200 characters.
		text := mdLinkRe.ReplaceAllString(strings.TrimSpace(line[hashes:]), "$1")
		anchor := ""
		if i := strings.LastIndex(text, "{#"); i >= 0 && strings.HasSuffix(text, "}") {
			anchor = text[i+2 : len(text)-1]
			text = strings.TrimSpace(text[:i])
			anchored++
		}
		if text == "" {
			continue
		}
		entries = append(entries, outlineEntry{depth: hashes, text: text, anchor: anchor, start: lineStart})
	}
	if total == 0 {
		return "This page has no headings, so it has no outline.\n"
	}
	// A section's size, so a model can tell before fetching whether one result
	// will hold it. Asked for by a model in the live pass, in those words.
	for i, e := range entries {
		end := len(md)
		for _, next := range entries[i+1:] {
			if next.depth <= e.depth {
				end = next.start
				break
			}
		}
		b.WriteString(strings.Repeat("  ", e.depth-1) + "- " + e.text)
		if e.anchor != "" {
			b.WriteString("  #" + e.anchor)
		}
		b.WriteString("  " + sizeHint(end-e.start) + "\n")
	}
	out := b.String()
	if anchored == 0 {
		return out + "\nNone of these headings has an anchor, so no section of this page can be " +
			"fetched on its own.\n"
	}
	// The real URL, not a placeholder. A model reading "as in
	// https://example.com/page#section-id" with two hundred real anchors above
	// it has to translate, and translation is where a doubt about *which* URL
	// the anchor belongs to comes from.
	example := "https://example.com/page"
	if pageURL != "" {
		if u, err := url.Parse(pageURL); err == nil {
			u.Fragment = ""
			example = u.String()
		}
	}
	// The first *anchored* entry, not the first entry: a page's opening
	// heading is often its title, which carries no anchor, and falling back to
	// a placeholder beside two hundred real ones is the translation this is
	// meant to remove.
	frag := "#section-id"
	for _, e := range entries {
		if e.anchor != "" {
			frag = "#" + e.anchor
			break
		}
	}
	example += frag
	return out + "\nFetch any of these sections on its own by adding its anchor to the URL, " +
		"as in " + example + "\n"
}

// htmlToMarkdown slims the page and converts it to markdown. It drops media
// (img/svg, which bloat with data: URIs — aider's slimdown_html does the same)
// and the two unambiguous chrome landmarks, nav and footer, which held the
// noise (Wikipedia's sidebar, page footers) in live tests. header stays: a
// blog/news article's <h1> title lives in an article <header>, and the chrome a
// page <header> carries is mostly <nav>, already removed. aside stays too:
// documentation generators render admonitions as <div class="admonition">, not
// <aside>, but Sphinx puts footnotes in <aside class="footnote">, so dropping it
// would lose real content. On a parse or conversion error it falls back to the
// (possibly slimmed) HTML.
func htmlToMarkdown(htmlStr, pageURL string, opts ScrapeOptions) string {
	fragMissing := ""
	fragEmpty := ""
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr)); err == nil {
		doc.Find("img, svg, script, style, noscript, nav, footer").Remove()
		// The same landmarks again, by ARIA role. Sphinx — the generator behind
		// the Python docs and much of the Go and Rust ecosystem's prose — marks
		// its navigation bars as <div class="related" role="navigation"> and
		// never emits a <nav>, so the element list alone leaves a breadcrumb
		// block at the top of every page and a second copy at the bottom.
		// Matching the role rather than the class catches every generator that
		// marks up honestly, which is the same bet the element list makes.
		doc.Find(`[role="navigation"], [role="banner"], [role="contentinfo"], [role="search"], [role="complementary"]`).Remove()
		resolveLinks(doc, pageURL)
		unwrapHeadingLists(doc)
		markHeadings(doc)
		// The fragment names a part of the page, so honor it before anything
		// downstream has to apologize for the size of the whole.
		if frag := fragmentOf(pageURL); frag != "" {
			switch part, text, ok := fragmentHTML(doc, frag); {
			case ok && text > 0:
				htmlStr = part
			case ok:
				// The anchor exists and names nothing that survives conversion.
				// Different from a missing anchor and worth saying so, because
				// the model's next move differs: the section is real, so the
				// name it has is not the one to fetch by. Whatever new shape a
				// generator invents, this is the branch that keeps the answer
				// from being an empty string — which is the one result a model
				// cannot act on or even report accurately.
				fragEmpty = frag
			default:
				// Said rather than silently ignored: a model that asked for a
				// section and got a page needs to know which it is holding.
				fragMissing = frag
			}
		}
		if fragMissing != "" || fragEmpty != "" || fragmentOf(pageURL) == "" {
			if slimmed, err := doc.Html(); err == nil {
				htmlStr = slimmed
			}
		}
	}
	md, err := htmltomarkdown.ConvertString(htmlStr)
	if err != nil {
		return htmlStr
	}
	md = emptyLinkRe.ReplaceAllString(md, "")
	// The converter escapes markdown punctuation in heading text, my anchor
	// included, so "{#Operator_precedence}" came out "{#Operator\_precedence}"
	// — a fragment that matches nothing. Four of fourteen documentation sites
	// were advertising anchors that could not work, which is worse than
	// advertising none.
	md = headingAnchorRe.ReplaceAllStringFunc(md, func(m string) string {
		return strings.ReplaceAll(m, `\`, "")
	})
	md = strings.TrimSpace(blankRunRe.ReplaceAllString(md, "\n\n"))
	if opts.Outline {
		return outlineOf(md, pageURL)
	}
	if fragEmpty != "" {
		return fmt.Sprintf("(%q is on this page but names no content of its own. Its "+
			"outline follows; fetch the section by the anchor listed there.)\n\n",
			"#"+fragEmpty) + outlineOf(md, pageURL)
	}
	if fragMissing != "" {
		// The outline, not the page. Handing back the whole thing meant a
		// truncated 58 KB prefix — mostly the table of contents — for a model
		// that had just said which section it wanted. Three of six models in a
		// live pass raised this unprompted, one calling it "the least helpful
		// thing possible"; they were right. The anchors that do exist are both
		// smaller and the only actionable answer.
		return fmt.Sprintf("(This page has no %q section. Its outline follows, so you can "+
			"pick an anchor that exists.)\n\n", "#"+fragMissing) + outlineOf(md, pageURL)
	}
	return md
}

// fragmentOf is the fragment of a URL, or "" if it has none or will not parse.
func fragmentOf(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	return u.Fragment
}

package coder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	return func(ctx context.Context, url string) (string, error) {
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
			text = htmlToMarkdown(text)
		}
		return fmt.Sprintf("Here is the content of %s:\n\n%s", url, text), nil
	}
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
func htmlToMarkdown(htmlStr string) string {
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr)); err == nil {
		doc.Find("img, svg, script, style, noscript, nav, footer").Remove()
		if slimmed, err := doc.Html(); err == nil {
			htmlStr = slimmed
		}
	}
	md, err := htmltomarkdown.ConvertString(htmlStr)
	if err != nil {
		return htmlStr
	}
	md = emptyLinkRe.ReplaceAllString(md, "")
	return strings.TrimSpace(blankRunRe.ReplaceAllString(md, "\n\n"))
}

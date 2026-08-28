package coder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
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
		// Where the content actually came from, which is not always where it
		// was asked for. The model picks the URL now, so a shortener or an
		// open redirect landing somewhere else is a thing the transcript should
		// record rather than something only the network saw.
		final := url
		if resp.Request != nil && resp.Request.URL != nil {
			final = resp.Request.URL.String()
		}
		if final != url {
			return wrapContent(url, text) + "\n\n(Redirected to " + final + ".)\n", nil
		}
		return wrapContent(url, text), nil
	}
}

// wrapContent frames a scraped page the way every scraper presents it to the
// model, so the phrasing stays identical across the HTTP and command paths.
func wrapContent(url, text string) string {
	return fmt.Sprintf("Here is the content of %s:\n\n%s", url, text)
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
	return func(ctx context.Context, url string) (string, error) {
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
		return wrapContent(url, htmlToMarkdown(stdout.buf.String())), nil
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

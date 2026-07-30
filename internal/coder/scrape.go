package coder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRunRe    = regexp.MustCompile(`\n{3,}`)
)

// SimpleScraper fetches a URL and reduces HTML to plain text with no proxy —
// the minimal v1 stand-in for aider's scraper.
var SimpleScraper = NewSimpleScraper(nil)

// NewSimpleScraper returns a Scraper that fetches over the given transport
// (nil => the default). The global proxy, when set, is threaded in this way so
// URL scraping honors it like every other outbound HTTPS action.
func NewSimpleScraper(transport http.RoundTripper) Scraper {
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	return func(ctx context.Context, url string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if err != nil {
			return "", err
		}
		text := string(body)
		if strings.Contains(resp.Header.Get("Content-Type"), "html") {
			text = scriptStyleRe.ReplaceAllString(text, "")
			text = tagRe.ReplaceAllString(text, "")
			text = blankRunRe.ReplaceAllString(text, "\n\n")
			text = strings.TrimSpace(text)
		}
		return fmt.Sprintf("Here is the content of %s:\n\n%s", url, text), nil
	}
}

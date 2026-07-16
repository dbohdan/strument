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

// SimpleScraper fetches a URL and reduces HTML to plain text — the minimal
// v1 stand-in for aider's scraper (basecoder-spec §1.4; see STATUS.md).
func SimpleScraper(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
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

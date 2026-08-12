package coder

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// urlRe matches aider's check_for_urls pattern.
var urlRe = regexp.MustCompile(`(https?://[^\s/$.?#].[^\s"]*[^\s,.])`)

// checkForUrls offers to scrape URLs in the input (minus rejectedUrls) and
// appends the content.
func (c *Coder) checkForUrls(ctx context.Context, inp string) string {
	if c.Scrape == nil {
		return inp
	}
	seen := map[string]bool{}
	var urls []string
	for _, u := range urlRe.FindAllString(inp, -1) {
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	sort.Strings(urls)
	var inpSb161 strings.Builder
	for _, url := range urls {
		if c.rejectedUrls[url] {
			continue
		}
		trimmed := strings.TrimRight(url, ".',\"")
		res := c.Confirm.Confirm(ConfirmRequest{
			Prompt:     "Add URL to the chat?",
			Subject:    trimmed,
			AllowNever: true,
			Group:      "add-url",
		})
		if res.Yes {
			content, err := c.Scrape(ctx, trimmed)
			if err != nil {
				c.Out.Errorf("Unable to fetch %s: %v", trimmed, err)
				continue
			}
			inpSb161.WriteString("\n\n" + content)
		} else {
			c.rejectedUrls[url] = true
		}
	}
	inp += inpSb161.String()
	return inp
}

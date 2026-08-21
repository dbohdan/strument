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
//
// Deliberately not gated by the sandbox, unlike bash and check. This runs on
// what the *user* typed — preprocUserInput is its only caller, and there is no
// tool a model can call to fetch a URL — so a scrape is the user's action,
// confirmed by the user, in the same class as /run.
//
// envallow.go does treat the scraper command as model-caused, and that is not a
// contradiction: it answers a different question. Filtering the environment is
// about what leaks *out*, since the fetched page lands in the model's context
// and a scraper that echoed its environment would carry the API key there. The
// execution gate is about what runs unconfined, and nothing the model can cause
// runs here.
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
			Prompt:  "Add URL to the chat?",
			Subject: trimmed,
			Group:   "add-url",
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

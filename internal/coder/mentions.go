package coder

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"
)

// addedFilesPrompt is aider prompts.added_files [Exact].
const addedFilesPrompt = "I added these files to the chat: %s\nLet me know if there are others we should add."

// fileMentions ports get_file_mentions: filenames the text plausibly refers
// to, by full relative path or unique basename (base_coder.py:1714).
func (c *Coder) fileMentions(content string, ignoreCurrent bool) map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.Fields(content) {
		w = strings.TrimRight(w, ",.!;:?")
		w = strings.Trim(w, "\"'`*_")
		words[w] = true
	}

	var addable []string
	existingBasenames := map[string]bool{}
	if ignoreCurrent {
		addable = c.allRelativeFiles()
	} else {
		addable = c.addableRelativeFiles()
		for _, f := range c.inchatRelativeFiles() {
			existingBasenames[path.Base(f)] = true
		}
		for _, f := range c.absReadOnlyFnames {
			existingBasenames[path.Base(c.relFname(f))] = true
		}
	}

	normalizedWords := map[string]bool{}
	for w := range words {
		normalizedWords[strings.ReplaceAll(w, "\\", "/")] = true
	}

	mentioned := map[string]bool{}
	fnameToRel := map[string][]string{}
	for _, relFname := range addable {
		if normalizedWords[strings.ReplaceAll(relFname, "\\", "/")] {
			mentioned[relFname] = true
		}
		base := path.Base(relFname)
		// Skip basenames that could be plain words like "run" or "make".
		if strings.ContainsAny(base, "/\\._-") {
			fnameToRel[base] = append(fnameToRel[base], relFname)
		}
	}
	for base, rels := range fnameToRel {
		if existingBasenames[base] {
			continue
		}
		if len(rels) == 1 && words[base] {
			mentioned[rels[0]] = true
		}
	}
	return mentioned
}

// checkForFileMentions offers to add mentioned files (minus ignoreMentions)
// and returns the added_files reflection message, or "" (§1.4, §2).
func (c *Coder) checkForFileMentions(content string) string {
	mentioned := c.fileMentions(content, false)

	var newMentions []string
	for f := range mentioned {
		if !c.ignoreMentions[f] {
			newMentions = append(newMentions, f)
		}
	}
	if len(newMentions) == 0 {
		return ""
	}
	sort.Strings(newMentions)

	var added []string
	for _, relFname := range newMentions {
		yes, never := c.Confirm.Confirm(ConfirmRequest{
			Prompt:     "Add file to the chat?",
			Subject:    relFname,
			AllowNever: true,
			Group:      "add-file",
		})
		if yes {
			c.AddFile(relFname)
			added = append(added, relFname)
		} else {
			c.ignoreMentions[relFname] = true
			_ = never
		}
	}
	if len(added) > 0 {
		return strings.Replace(addedFilesPrompt, "%s", strings.Join(added, ", "), 1)
	}
	return ""
}

// identMentions splits on non-word characters, like aider's \W+ split.
var nonWordRe = regexp.MustCompile(`\W+`)

func identMentions(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range nonWordRe.Split(text, -1) {
		if w != "" {
			out[w] = true
		}
	}
	return out
}

// identFilenameMatches maps mentioned identifiers to files whose stem
// matches (>= 5 chars), per get_ident_filename_matches.
func (c *Coder) identFilenameMatches(idents map[string]bool) map[string]bool {
	byStem := map[string][]string{}
	for _, fname := range c.allRelativeFiles() {
		if fname == "" || fname == "." {
			continue
		}
		base := path.Base(fname)
		stem := strings.ToLower(strings.TrimSuffix(base, path.Ext(base)))
		if len(stem) >= 5 {
			byStem[stem] = append(byStem[stem], fname)
		}
	}
	matches := map[string]bool{}
	for ident := range idents {
		if len(ident) < 5 {
			continue
		}
		for _, f := range byStem[strings.ToLower(ident)] {
			matches[f] = true
		}
	}
	return matches
}

// urlRe matches aider's check_for_urls pattern.
var urlRe = regexp.MustCompile(`(https?://[^\s/$.?#].[^\s"]*[^\s,.])`)

// checkForUrls offers to scrape URLs in the input (minus rejectedUrls) and
// appends the content (§1.4).
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
	for _, url := range urls {
		if c.rejectedUrls[url] {
			continue
		}
		trimmed := strings.TrimRight(url, ".',\"")
		yes, _ := c.Confirm.Confirm(ConfirmRequest{
			Prompt:     "Add URL to the chat?",
			Subject:    trimmed,
			AllowNever: true,
			Group:      "add-url",
		})
		if yes {
			content, err := c.Scrape(ctx, trimmed)
			if err != nil {
				c.Out.Error("Unable to fetch %s: %v", trimmed, err)
				continue
			}
			inp += "\n\n" + content
		} else {
			c.rejectedUrls[url] = true
		}
	}
	return inp
}

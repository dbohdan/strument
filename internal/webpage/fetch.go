// Package webpage fetches a page for a model to read: over HTTP, or through a
// user's scraper command, with HTML converted to Markdown, a URL fragment
// narrowed to its section, a range narrowed to its lines, and an outline of
// any page in place of its text when asked. It refuses local addresses it was
// not cleared for and redirects to origins it was not approved for, and says
// so in errors the coder turns into a question or an answer.
//
// The coder's webfetch tool decides what to fetch and how much of the answer
// fits one tool result.
package webpage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Outline is the map handed back with an oversized page, built by the
// outline builder the page's own kind has: heading anchors for HTML and
// Markdown, definition spans for code, a line count for anything else.
//
// It runs on the body the scraper fetched — before Wrap's framing line
// — because the builders answer in line numbers and anchors of the *file*, and
// a framing line added two lines to the top would shift every one of them. A
// body can only be classified where the kind is still known, so the kind
// travels from renderFetched as the third return value.
func Outline(pageURL, body string, kind Kind) string {
	switch kind {
	case KindHTML, KindMarkdown:
		// The HTML outline answers in URL fragments, which are how an HTML or
		// Markdown page is fetched; the Markdown builders answer in line
		// ranges, which is how a text page is fetched.
		if kind == KindHTML {
			return outlineOf(body, pageURL)
		}
		return markdownOutline(body)
	case KindCode:
		return defOutlineOf(pageURL, body, 0)
	default:
		return plainTextOutline(body, 0)
	}
}

// Unwrap reverses Wrap's framing line, so an outline computed
// over the returned text answers in the file's own line numbers. The framing
// is deterministic (a fixed prefix and a trailing newline), so exact reversal
// is a Cut, not a parse.
func Unwrap(wrapped string) string {
	if rest, ok := strings.CutPrefix(wrapped, "Here is the content of "); ok {
		if _, body, ok := strings.Cut(rest, ":\n\n"); ok {
			return body
		}
	}
	return wrapped
}

// ParseLineRange reads "80-120" (1-based, inclusive) or a single "80".
func ParseLineRange(s string) (lo, hi int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	first, second, _ := strings.Cut(s, "-")
	lo, err = strconv.Atoi(strings.TrimSpace(first))
	if err != nil || lo < 1 {
		return 0, 0, fmt.Errorf("%q is not a line number", first)
	}
	hi = lo
	if second != "" {
		hi, err = strconv.Atoi(strings.TrimSpace(second))
		if err != nil {
			return 0, 0, fmt.Errorf("%q is not a line number", second)
		}
	} else if strings.HasSuffix(s, "-") {
		return 0, 0, fmt.Errorf("the range %s has no end", s)
	}
	if hi < lo {
		return 0, 0, fmt.Errorf("the range %s ends before it starts", s)
	}
	return lo, hi, nil
}

// Options are the ways a caller can ask for less than a whole page.
//
// The fragment is not here: it rides in the URL, where the model writes it and
// where every link on the page already carries one. Only the outline needs
// saying separately.
type Options struct {
	// Outline returns the page's headings and their anchors instead of its
	// content — the map you read before deciding which section to fetch.
	Outline bool

	// Range narrows a plain-text page to the given 1-based lines, inclusive —
	// "80-120". It is how a model navigates a fetched file that has no
	// anchors: the outline of a text page advertises line numbers, so the
	// fetch that follows them takes this.
	Range string

	// Follow decides whether a redirect to another origin is followed. nil
	// follows every redirect, which is right for /web: the user typed that URL
	// and reads what comes back. webfetch sets it, because the model picked
	// the URL and the user approved one origin — an open redirect on it must
	// not carry the fetch to an origin nobody approved. A redirect within the
	// same origin is always followed; this is asked only about the others.
	Follow func(url string) bool

	// LocalOK decides whether a connection to a local address may go ahead,
	// given the origin (host:port) the URL named. nil allows every one, which
	// is right for /web and for a fetch the user approved at a prompt. See
	// localaddr.go. A `scraper` command does its own networking, so this
	// binds the built-in fetcher only.
	LocalOK func(origin string) bool
}

// lineRange is the parsed Range field: (0, 0) when unset.
func (o Options) lineRange() (lo, hi int) {
	if o.Range == "" {
		return 0, 0
	}
	lo, hi, err := ParseLineRange(o.Range)
	if err != nil {
		return 0, 0
	}
	return lo, hi
}

// Scraper fetches URL content; injectable for tests.
type Scraper func(ctx context.Context, url string, opts Options) (string, error)

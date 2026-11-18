// Package websearch runs a web search through one of the services a user can
// configure: their own SearXNG instance, Exa, or AnySearch. Each is a Searcher,
// the port the coder's websearch tool calls, and each reduces its service's
// answer to the same Results.
package websearch

import "context"

// Shared by every backend, because they are limits on what a tool result can
// usefully carry rather than facts about any one service.
const (
	// Twenty per page is what a SearXNG instance returns and ten is AnySearch's
	// own cap; ten is also what a model can read without the tail crowding out
	// the answer. A query that needs the eleventh result needs a better query.
	maxResults = 10
	maxBytes   = 4 * 1024 * 1024
	// A snippet is text from whatever page ranked, so its length is not the
	// instance's decision and not ours — a live instance returned about ninety
	// characters, and nothing stops a page returning a megabyte. Unbounded, one
	// result could push the other nine and the note about unresponsive engines
	// past the tool-result cap, which trims from the tail. Bounding each
	// snippet keeps the whole result small enough that nothing is ever cut.
	maxSnippet = 400
)

// Results is one answered query, already reduced to the fields worth
// carrying. A real result arrives with twenty-three of them — positions,
// parsed_url, open_group, thumbnail, score — and all but three are noise to a
// reader.
type Results struct {
	Query   string
	Results []Result
	Answers []string
	// Unresponsive pairs an engine with why it did not answer. It is the whole
	// reason this type is not just []Result: on a real instance, a
	// perfectly good query came back with brave rate-limited, duckduckgo
	// showing a CAPTCHA and startpage timing out — and a query with no results
	// came back the same way. Without this, "the web has nothing" and "three
	// engines were broken" are the same answer, and a model will report the
	// first when it is looking at the second.
	Unresponsive []UnresponsiveEngine
}

// Result is one hit.
type Result struct {
	Title, URL, Content, Published string
}

// UnresponsiveEngine is one engine that did not answer, and why.
type UnresponsiveEngine struct{ Engine, Reason string }

// Searcher runs one query; injectable for tests.
type Searcher func(ctx context.Context, query string) (Results, error)

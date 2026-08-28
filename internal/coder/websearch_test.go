package coder

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func searchCall(query string) llm.ToolCall {
	return llm.ToolCall{ID: "call_1", Name: toolWebsearch, Arguments: `{"query":` + jsonString(query) + `}`}
}

func runSearch(t *testing.T, c *Coder, tc llm.ToolCall) string {
	t.Helper()
	q, msg := parseSearchArgs(tc)
	if msg != "" {
		return msg
	}
	return c.runWebsearch(context.Background(), q)
}

func searchCoder(t *testing.T, res SearchResults) *Coder {
	t.Helper()
	c := testCoder(t)
	c.Confirm = &recordingConfirmer{answer: true}
	c.Search = func(context.Context, string) (SearchResults, error) { return res, nil }
	return c
}

// The finding that shaped the whole result format. On a live instance a query
// with no hits came back with three engines unresponsive — so "nothing is
// written about this" and "your search was broken" are the same response
// unless the failures are reported. A model reading only "No results" will
// tell the user the web is empty while holding a broken search.
func TestSearchWithNoResultsSaysTheEnginesFailed(t *testing.T) {
	c := searchCoder(t, SearchResults{
		Query: "obscure thing",
		Unresponsive: []UnresponsiveEngine{
			{"brave", "too many requests"}, {"duckduckgo", "CAPTCHA"}, {"startpage", "timeout"},
		},
	})
	out := runSearch(t, c, searchCall("obscure thing"))

	if strings.TrimSpace(out) == "" {
		t.Fatal("an empty result — the one answer a model cannot act on")
	}
	if !strings.Contains(out, "No results") {
		t.Errorf("did not say there were no results:\n%s", out)
	}
	for _, engine := range []string{"brave", "duckduckgo", "startpage", "CAPTCHA"} {
		if !strings.Contains(out, engine) {
			t.Errorf("did not name %q among the failures:\n%s", engine, out)
		}
	}
	// And it has to say what that means, or the list is trivia.
	if !strings.Contains(out, "broken search") {
		t.Errorf("did not distinguish a broken search from an empty subject:\n%s", out)
	}
	// The failure comes *before* "No results", because that is the order a
	// model summarising tersely reads in. Asked the same question twice with
	// the same tool result, one answer carried the blocked engines and the
	// other said only "the search worked, zero results" — the note was last
	// and parenthesised then.
	if strings.Index(out, "degraded") > strings.Index(out, "No results") {
		t.Errorf("the engine failures trail the empty result:\n%s", out)
	}
}

// Three engines down on a *successful* query is the normal state, not an
// alarm, so it is reported and framed differently: the results are thin, not
// absent.
func TestSearchWithResultsCallsFailuresThinRatherThanFailed(t *testing.T) {
	c := searchCoder(t, SearchResults{
		Query:        "q",
		Results:      []SearchResult{{Title: "net/http", URL: "https://pkg.go.dev/net/http", Content: "Package http."}},
		Unresponsive: []UnresponsiveEngine{{"brave", "too many requests"}},
	})
	out := runSearch(t, c, searchCall("q"))

	if !strings.Contains(out, "thinner") || strings.Contains(out, "degraded") {
		t.Errorf("a query with hits was framed as a failure:\n%s", out)
	}
	if !strings.Contains(out, "https://pkg.go.dev/net/http") || !strings.Contains(out, "Package http.") {
		t.Errorf("the result itself did not come through:\n%s", out)
	}
}

// The "a" is turn-scoped and covers every origin, which inverts webfetch on
// both counts: the destination is pinned by config so there is nothing to
// scope per origin, and a turn holds many searches so the turn is the unit
// that pays. If this ever needs a second prompt in one turn, the group broke.
func TestSearchAlwaysCoversTheTurnAndDiesWithIt(t *testing.T) {
	c := searchCoder(t, SearchResults{Query: "q"})
	ac := &alwaysConfirmer{}
	c.Confirm = ac

	runSearch(t, c, searchCall("first"))
	runSearch(t, c, searchCall("second — a different query entirely"))
	if len(ac.got) != 1 {
		t.Errorf("asked %d times in one turn, want 1", len(ac.got))
	}
	if ac.got[0].Query != "first" {
		t.Errorf("the prompt did not carry the query: %+v", ac.got[0])
	}
	// Plain --yes covers it: the model never picks where a search goes.
	if ac.got[0].RequiresYesShell {
		t.Error("search withheld itself from --yes; only the model's own choice of destination earns that")
	}

	c.initBeforeMessage()
	runSearch(t, c, searchCall("next turn"))
	if len(ac.got) != 2 {
		t.Errorf("asked %d times, want 2 — the grant outlived its turn", len(ac.got))
	}
	if len(c.SessionOrigins()) != 0 {
		t.Error("search wrote into the session map, which only webfetch may use")
	}
}

// A search nobody was asked about still has to be seen, the same rule webfetch
// follows for an allowlisted origin: an "a" buys fewer questions, not less to
// read.
func TestSearchAnnouncesTheQueriesItWasNotAskedAbout(t *testing.T) {
	c := searchCoder(t, SearchResults{Query: "q"})
	c.Confirm = &alwaysConfirmer{}
	out := &captureOutput{}
	c.Out = out

	runSearch(t, c, searchCall("the first one"))
	out.reset()
	runSearch(t, c, searchCall("the second one"))
	if !strings.Contains(out.String(), "the second one") {
		t.Errorf("an unprompted search left no trace:\n%s", out.String())
	}
}

// A declined search is a sentence, and a missing query is a reflection rather
// than a prompt for a string that was never going to search for anything.
func TestSearchDeclinedAndEmptyQuery(t *testing.T) {
	c := searchCoder(t, SearchResults{Query: "q"})
	c.Confirm = &recordingConfirmer{answer: false}
	if out := runSearch(t, c, searchCall("q")); !strings.Contains(out, "chose not to") {
		t.Errorf("declined search said %q", out)
	}
	if out := runSearch(t, c, llm.ToolCall{ID: "x", Name: toolWebsearch, Arguments: `{"query":"  "}`}); !strings.Contains(out, "missing") {
		t.Errorf("empty query said %q", out)
	}
}

// The tool is offered only with a backend configured — genuinely conditional,
// unlike webfetch, which always has the built-in fetcher behind it.
func TestWebsearchToolOfferedOnlyWhenConfigured(t *testing.T) {
	c := testCoder(t)
	if slices2Contains(c.toolDefs(), toolWebsearch) {
		t.Error("websearch was offered with no instance configured")
	}
	c.Search = func(context.Context, string) (SearchResults, error) { return SearchResults{}, nil }
	if !slices2Contains(c.toolDefs(), toolWebsearch) {
		t.Error("websearch was not offered with an instance configured")
	}
}

func slices2Contains(defs []llm.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// Ask mode keeps search, and that is the point of the mode rather than an
// exception to it: finding out what exists is what a discussion turn is for,
// and a search mutates nothing. The mutating tools stay withheld.
func TestWebsearchSurvivesAskMode(t *testing.T) {
	c := testCoder(t)
	c.Search = func(context.Context, string) (SearchResults, error) { return SearchResults{}, nil }
	c.editFormat = "ask"
	defs := c.toolDefs()
	if !slices2Contains(defs, toolWebsearch) {
		t.Error("ask mode dropped websearch")
	}
	for _, mutating := range []string{toolBash, toolEdit} {
		if slices2Contains(defs, mutating) {
			t.Errorf("ask mode offered %q", mutating)
		}
	}
}
